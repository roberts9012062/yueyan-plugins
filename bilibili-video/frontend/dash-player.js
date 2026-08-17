// cmd/bilibili-video-plugin/frontend/dash-player.js
// DASH（音视频分离 m4s）MSE 播放器：B 站 1080P 仅有 DASH 形态（mp4/durl 上限 720P，
// 实测结论），浏览器原生 video 无法直接播分离流——经 MediaSource 双 SourceBuffer
// 装载（视频/音频各一条 fMP4，MSE 按时间戳自动对轨）。
// 拉取策略：Range 分块（4MB/块）+ 失败重试——B 站 CDN 对连续大流量全量拉取会
// 503 限流，分块小请求稳定且内存可控；块粒度失败可重试当前块。
// 流地址统一经宿主同源代理（/video/bilibili/stream，Range 透传 206）。
// 约束：纯原生 ESM + 浏览器标准 API（无构建链）；基础版进度条拖动限已缓冲区。

// CHUNK_SIZE 单块拉取字节数（4MB：CDN 友好且 append 开销可控）。
const CHUNK_SIZE = 4 << 20;

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
  return pool.reduce((best, s) => (Number(s.id) > Number(best.id) ? s : best), pool[0]);
}

// waitUpdateEnd 等 SourceBuffer 结束当前更新（纯辅助）。
function waitUpdateEnd(sb) {
  return new Promise((resolve) => {
    if (!sb.updating) {
      resolve();
      return;
    }
    sb.addEventListener("updateend", () => resolve(), { once: true });
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

// streamInto 分块拉取并 append（返回是否完整装载；aborted 可中断）。
async function streamInto(sb, url, aborted) {
  const total = await probeTotal(url);
  if (total <= 0) {
    throw new Error("流长度探测失败");
  }
  for (let pos = 0; pos < total; ) {
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
    pos = end + 1;
  }
  await waitUpdateEnd(sb);
  return true;
}

// playDash 以 MSE 播放 DASH 流组（装载完成 resolve；返回控制器供中断/清理）。
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
      const videoTask = streamInto(vsb, proxy(videoStream.base_url), aborted);
      const audioTask = asb ? streamInto(asb, proxy(audioStream.base_url), aborted) : Promise.resolve(true);
      const [videoDone] = await Promise.all([videoTask, audioTask]);
      if (videoDone && !aborted.aborted) {
        if (!vsb.updating && !(asb && asb.updating)) {
          try {
            ms.endOfStream();
          } catch (e) {
            /* endOfStream 竞态可忽略（流已装载完成） */
          }
        }
        videoEl.play().catch(() => {}); // autoplay 被策略阻止时经原生控件播放
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
