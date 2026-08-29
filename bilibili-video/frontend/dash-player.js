// cmd/bilibili-video-plugin/frontend/dash-player.js
// DASH（音视频分离 m4s）MSE 播放器：B 站 1080P 仅有 DASH 形态（mp4/durl 上限 720P，
// 实测结论），浏览器原生 video 无法直接播分离流——经 MediaSource 双 SourceBuffer
// 装载（视频/音频各一条 fMP4，MSE 按时间戳自动对轨）。
// 拉取策略：流式渐进 append + 候选节点轮换——
//   - 流式：fetch Range 响应体经 reader 边读边 appendBuffer（分片 16-64KB），
//     首片到达即装载开播（起播门槛几十 KB；低速中转链路下整块等待需数分钟不可用）；
//   - 轮换：B 站按 playurl 请求方（服务器机房 IP）分配的主节点可能对机房 IP 403
//     风控，backup 节点（upos-*.akamaized.net 等）通常放行——SEGMENT 段粒度失败
//     重试/切换候选，段内连接中断按已推进字节断点续传。
// 内存策略（滑窗渐进）：随播放推进定期 remove 释放已播区域（KEEP_SEC），未播预
// 缓冲超过 AHEAD_SEC 时暂停读取背压等待——稳态 SourceBuffer 占用约
// (KEEP+AHEAD) 秒码率，规避 Chrome ~150MB 配额的 QuotaExceededError。
// 流地址统一经宿主同源代理（/video/bilibili/stream，Range 透传 206）。
// 约束：纯原生 ESM + 浏览器标准 API（无构建链）；基础版进度条拖动限已缓冲区。

// SEGMENT 单段拉取字节数（段 = 轮换/续传容错单位；段内流式增量 append）。
const SEGMENT = 4 << 20;

// KEEP_SEC 已播区域保留秒数（此前部分可释放；拖回进度条的重播诉求由重载兜底）。
const KEEP_SEC = 20;

// AHEAD_SEC 未播预缓冲秒数上限（超过暂停读取，播放推进后继续——防全量堆积）。
const AHEAD_SEC = 90;

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

// fetchRangeRetry 单节点带重试的 Range 请求（CDN 限流 503 等瞬时错误重试；retries 次）。
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

// streamCandidates 流地址候选列表（base 在前、backup 随后；经代理包装 + 去重）。
function streamCandidates(stream, proxy) {
  const urls = [stream.base_url].concat(stream.backup_urls || []);
  const seen = new Set();
  const out = [];
  for (const u of urls) {
    const wrapped = proxy(u);
    if (wrapped && !seen.has(wrapped)) {
      seen.add(wrapped);
      out.push(wrapped);
    }
  }
  return out;
}

// fetchSegment 候选轮换的段请求：从 curIdx 起当前节点重试 retries 次仍失败
// （403 风控 / 503 限流）则切换下一候选；返回响应与命中候选下标（全部耗尽抛最后错误）。
async function fetchSegment(candidates, curIdx, start, end, retries) {
  let lastErr = null;
  for (let i = curIdx; i < candidates.length; i++) {
    try {
      const resp = await fetchRangeRetry(candidates[i], start, end, retries);
      return { resp: resp, idx: i };
    } catch (e) {
      lastErr = e;
    }
  }
  throw lastErr;
}

// probeTotal 用 1 字节 Range 探测流总长（Content-Range: bytes 0-0/N）+ 首个可用候选下标。
async function probeTotal(candidates) {
  const hit = await fetchSegment(candidates, 0, 0, 0, 2);
  const cr = hit.resp.headers.get("Content-Range") || "";
  if (cr.indexOf("/") >= 0) {
    const total = Number(cr.split("/").pop());
    if (total > 0) {
      return { total: total, idx: hit.idx };
    }
  }
  return { total: 0, idx: hit.idx };
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

// streamInto 流式拉取并渐进 append（返回是否完整装载；aborted 可中断）。
// 段（SEGMENT）为轮换/续传容错单位；段内响应体经 reader 边读边 append——
// 首片到达即触发 onFirstAppend（提前开播，不等整段）；段内连接中断抛出后由
// 外层循环按已推进字节断点续传（换候选或同节点重试）。
async function streamInto(sb, candidates, aborted, videoEl, onFirstAppend) {
  const probed = await probeTotal(candidates);
  if (probed.total <= 0) {
    throw new Error("流长度探测失败");
  }
  const total = probed.total;
  let pos = 0;
  let curIdx = probed.idx;
  let firstNotified = false;
  while (pos < total) {
    if (aborted.aborted) {
      return false;
    }
    const segEnd = Math.min(pos + SEGMENT, total) - 1;
    let hit;
    try {
      hit = await fetchSegment(candidates, curIdx, pos, segEnd, 1);
    } catch (e) {
      throw new Error("拉取失败 @" + pos + "：" + String(e && e.message ? e.message : e));
    }
    curIdx = hit.idx;
    // 段内流式读取：中断（read 抛错）跳回外层 while 按 pos 断点续传
    try {
      const reader = hit.resp.body.getReader();
      for (;;) {
        if (aborted.aborted) {
          reader.cancel().catch(() => {});
          return false;
        }
        const step = await reader.read();
        if (step.done) {
          break;
        }
        await waitUpdateEnd(sb);
        if (aborted.aborted) {
          return false;
        }
        sb.appendBuffer(step.value);
        pos += step.value.byteLength;
        if (!firstNotified) {
          firstNotified = true;
          if (onFirstAppend) {
            onFirstAppend();
          }
        }
        evictPlayed(sb, videoEl);
        // 背压：未播预缓冲超出窗口时暂停读取（防全量堆积击穿 MSE 配额）
        while (!aborted.aborted && bufferedEnd(sb) - (videoEl.currentTime || 0) > AHEAD_SEC) {
          await sleep(800);
        }
      }
    } catch (e) {
      if (aborted.aborted) {
        return false;
      }
      if (pos >= total) {
        break;
      }
      // 连接中断：短暂退避后续传（外层 while 从当前 pos 重拉）
      await sleep(700);
    }
  }
  await waitUpdateEnd(sb);
  return true;
}

// playDash 以 MSE 播放 DASH 流组（首片到达即开播；返回控制器供中断/清理）。
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
  const videoCands = streamCandidates(videoStream, proxy);
  const audioCands = audioStream ? streamCandidates(audioStream, proxy) : null;
  const ms = new MediaSource();
  const objectURL = URL.createObjectURL(ms);
  videoEl.src = objectURL;

  const setup = async () => {
    try {
      const vsb = ms.addSourceBuffer('video/mp4; codecs="' + (videoStream.codecs || "avc1.640028") + '"');
      const asb = audioCands
        ? ms.addSourceBuffer('audio/mp4; codecs="' + (audioStream.codecs || "mp4a.40.2") + '"')
        : null;
      // 首片视频装载完成即开播（不等整段；音视频按时间戳自动对轨）
      const videoTask = streamInto(vsb, videoCands, aborted, videoEl, () => {
        videoEl.play().catch(() => {}); // autoplay 被策略阻止时经原生控件播放
      });
      const audioTask = asb ? streamInto(asb, audioCands, aborted, videoEl, null) : Promise.resolve(true);
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
