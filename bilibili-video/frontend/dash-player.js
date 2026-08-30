// cmd/bilibili-video-plugin/frontend/dash-player.js
// DASH（音视频分离 m4s）MSE 播放器：B 站 1080P 仅有 DASH 形态（mp4/durl 上限 720P，
// 实测结论），浏览器原生 video 无法直接播分离流——经 MediaSource 双 SourceBuffer
// 装载（视频/音频各一条 fMP4，MSE 按时间戳自动对轨）。
// 拉取策略：Range 分块（4MB/块）+ 失败重试——B 站 CDN 对连续大流量全量拉取会
// 503 限流，分块小请求稳定且内存可控；块粒度失败可重试当前块。
// 内存策略（滑窗渐进）：首块就绪即开播（不等全量）；随播放推进定期 remove 释放
// 已播区域（KEEP_SEC），未播预缓冲超过 AHEAD_SEC 时暂停拉取背压等待——稳态
// SourceBuffer 占用约 (KEEP+AHEAD) 秒码率（1080P 约 30-40MB），彻底规避
// Chrome ~150MB 配额的 QuotaExceededError（全量装载长视频必爆）。
// 流地址统一经宿主同源代理（/video/bilibili/stream，Range 透传 206）。
// 约束：纯原生 ESM + 浏览器标准 API（无构建链）；基础版进度条拖动限已缓冲区。

// CHUNK_SIZE 单块拉取字节数（4MB：CDN 友好且 append 开销可控）。
const CHUNK_SIZE = 4 << 20;

// KEEP_SEC 已播区域保留秒数（此前部分可释放；拖回进度条的重播诉求由重载兜底）。
const KEEP_SEC = 20;

// AHEAD_SEC 未播预缓冲秒数上限（超过暂停拉取，播放推进后继续——防全量堆积）。
const AHEAD_SEC = 60;

// sleep 毫秒等待（纯辅助）。
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// pickVideoStream 选视频流：优先 avc1 编码（浏览器兼容最好），目标 qn 不存在时取最高可达档。
export function pickVideoStream(videoStreams, targetQn) {
  const avc = videoStreams.filter((s) => (s.codecs || "").startsWith("avc1"));
  const pool = avc.length > 0 ? avc : videoStreams;
  const hit = pool.find((s) => Number(s.id) === Number(targetQn));
  if (hit) {
    return hit;
  }
  return pool.reduce((best, s) => (Number(s.id) > Number(best.id) ? best : s), pool[0]);
}

// waitUpdateEnd 等 SourceBuffer 结束当前更新（append/remove 共用 updating 态）。
// 同时监听 error/abort：数据解析失败时 MSE 不发 updateend 只发 error——
// 只等 updateend 会永久挂起（表现为装载静默卡死、提示条无变化），
// 此处将错误上抛（含 SourceBuffer.error 解码详情）供 onError 展示。
function waitUpdateEnd(sb) {
  return new Promise((resolve, reject) => {
    if (!sb.updating) {
      if (sb.error) {
        reject(new Error("解码失败(" + sb.error.code + ")"));
        return;
      }
      resolve();
      return;
    }
    const done = () => { cleanup(); resolve(); };
    const fail = () => { cleanup(); reject(new Error(sb.error ? "解码失败(" + sb.error.code + ":" + (sb.error.message || "") + ")" : "更新中止")); };
    const cleanup = () => {
      sb.removeEventListener("updateend", done);
      sb.removeEventListener("error", fail);
      sb.removeEventListener("abort", fail);
    };
    sb.addEventListener("updateend", done, { once: true });
    sb.addEventListener("error", fail, { once: true });
    sb.addEventListener("abort", fail, { once: true });
  });
}

// fetchRangeRetry 带重试的 Range 请求（CDN 限流 503 等瞬时错误重试；retries 次）。
async function fetchRangeRetry(url, start, end, retries) {
  let lastErr = null;
  for (let i = 0; i <= retries; i++) {
    try {
      const resp = await fetch(url, { headers: { Range: "bytes=" + start + "-" + end } });
      if (resp.ok || resp.status === 206) {
        return resp;
      }
      lastErr = new Error("HTTP " + resp.status);
    } catch (e) {
      lastErr = e;
    }
    if (i < retries) {
      await sleep(600 * (i + 1));
    }
  }
  throw lastErr;
}

// probeTotal 用 1 字节 Range 探测流总长（Content-Range: bytes 0-0/N）。
async function probeTotal(url) {
  const resp = await fetchRangeRetry(url, 0, 0, 2);
  const cr = resp.headers.get("Content-Range") || "";
  if (cr.indexOf("/") >= 0) {
    const total = Number(cr.split("/").pop());
    if (total > 0) {
      return total;
    }
  }
  return 0;
}

// evictPlayed 释放已播区域（currentTime 之前 KEEP_SEC 秒以前的部分）。
// remove 为异步更新：发出即可，由下轮 waitUpdateEnd 串行化兜底。
function evictPlayed(sb, videoEl) {
  if (sb.updating || sb.buffered.length === 0) {
    return;
  }
  const cur = videoEl.currentTime || 0;
  const start = sb.buffered.start(0);
  const evictTo = cur - KEEP_SEC;
  if (evictTo > start + 5) {
    try {
      sb.remove(start, evictTo);
    } catch (e) {
      /* 未在 updating 态之外调用等竞态：跳过本轮释放 */
    }
  }
}

// bufferedEnd 取缓冲末端时间（无缓冲返回 0；纯辅助）。
function bufferedEnd(sb) {
  return sb.buffered.length > 0 ? sb.buffered.end(sb.buffered.length - 1) : 0;
}

// streamInto 分块拉取并滑窗 append（返回是否完整装载；aborted 可中断）。
// videoEl 供释放窗口对齐播放位置；onFirstAppend 首块装载完成回调（提前开播）。
async function streamInto(sb, url, aborted, videoEl, onFirstAppend) {
  const total = await probeTotal(url);
  if (total <= 0) {
    throw new Error("流长度探测失败");
  }
  let firstNotified = false;
  for (let pos = 0; pos < total; ) {
    if (aborted.aborted) {
      return false;
    }
    // 背压：未播预缓冲超出窗口时等待播放推进（防全量堆积击穿 MSE 配额）
    while (!aborted.aborted && bufferedEnd(sb) - (videoEl.currentTime || 0) > AHEAD_SEC) {
      await sleep(800);
    }
    if (aborted.aborted) {
      return false;
    }
    const end = Math.min(pos + CHUNK_SIZE, total) - 1;
    const resp = await fetchRangeRetry(url, pos, end, 3);
    const chunk = await resp.arrayBuffer();
    if (chunk.byteLength === 0) {
      throw new Error("空块响应");
    }
    await waitUpdateEnd(sb);
    if (aborted.aborted) {
      return false;
    }
    sb.appendBuffer(chunk);
    await waitUpdateEnd(sb);
    evictPlayed(sb, videoEl);
    pos = end + 1;
    if (!firstNotified) {
      firstNotified = true;
      if (onFirstAppend) {
        onFirstAppend();
      }
    }
  }
  await waitUpdateEnd(sb);
  return true;
}

// playDash 以 MSE 播放 DASH 流组（首块就绪即开播；返回控制器供中断/清理）。
// 参数：videoEl 目标 video 元素；dash 后端 DashGroup；targetQn 目标清晰度；
//      proxy 流地址代理函数；onError 播放失败回调。
export function playDash(videoEl, dash, targetQn, proxy, onError) {
  const aborted = { aborted: false };
  const videoStream = pickVideoStream(dash.video || [], targetQn);
  const audioStream = (dash.audio || [])[0];
  if (!videoStream) {
    onError("无可用视频流");
    return { quality: 0, stop: () => { aborted.aborted = true; } };
  }
  const ms = new MediaSource();
  const objectURL = URL.createObjectURL(ms);
  videoEl.src = objectURL;

  const setup = async () => {
    try {
      const vsb = ms.addSourceBuffer('video/mp4; codecs="' + (videoStream.codecs || "avc1.640028") + '"');
      const asb = audioStream
        ? ms.addSourceBuffer('audio/mp4; codecs="' + (audioStream.codecs || "mp4a.40.2") + '"')
        : null;
      // 首块视频装载完成即开播（不等全量；音视频按时间戳自动对轨）
      const videoTask = streamInto(vsb, proxy(videoStream.base_url), aborted, videoEl, () => {
        videoEl.play().catch(() => {}); // autoplay 被策略阻止时经原生控件播放
      });
      const audioTask = asb ? streamInto(asb, proxy(audioStream.base_url), aborted, videoEl, null) : Promise.resolve(true);
      const [videoDone] = await Promise.all([videoTask, audioTask]);
      if (videoDone && !aborted.aborted) {
        if (!vsb.updating && !(asb && asb.updating)) {
          try {
            ms.endOfStream();
          } catch (e) {
            /* endOfStream 竞态可忽略（流已装载完成） */
          }
        }
      }
    } catch (e) {
      if (!aborted.aborted) {
        onError("DASH 播放失败：" + String(e));
      }
    }
  };
  ms.addEventListener("sourceopen", () => { void setup(); }, { once: true });

  return {
    quality: Number(videoStream.id),
    stop: () => {
      aborted.aborted = true;
      URL.revokeObjectURL(objectURL);
    },
  };
}
