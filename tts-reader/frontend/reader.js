// marketplace-repo/tts-reader/frontend/reader.js
// 朗读插件 · 前端扩展（原生 ESM，CSP 兼容，无外部资源）。双槽位：
//   - post.footer：帖子详情页脚完整工具条（朗读/暂停/停止/倍速/进度）
//   - post.card.actions：时间线卡片操作区迷你控件（单按钮朗读标题+摘要）
// 流程：POST /api/v1/tts 合成 → {id}（宿主公开桥接，访客无需登录）→
//       POST /api/v1/tts/audio {id} 取音频字节 → Blob → objectURL → <audio> 播放。
// 契约：默认导出 register(ctx)，返回清理函数。ctx: { slot, el, api, user, props }
export default function register(ctx) {
  if (ctx.slot === "post.card.actions") {
    return registerCardAction(ctx);
  }
  return registerFooter(ctx);
}

// ---------- 共享：合成与音频拉取 ----------

// fetchAudioBlob 合成并拉取音频字节 → Blob URL（纯数据通道；失败抛 Error）。
async function fetchAudioBlob(text) {
  const synthRes = await fetch("/api/v1/tts", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ text }),
  });
  const synth = await synthRes.json();
  if (!synth || synth.error || !synth.id) {
    throw new Error((synth && synth.error) || "合成失败，请稍后再试");
  }
  const audioRes = await fetch("/api/v1/tts/audio", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id: synth.id }),
  });
  if (!audioRes.ok) {
    throw new Error("音频拉取失败（" + audioRes.status + "）");
  }
  const buf = await audioRes.arrayBuffer();
  return URL.createObjectURL(new Blob([buf], { type: "audio/mpeg" }));
}

// cardSpeechText 卡片朗读文本（标题 + 摘要；压缩空白限 1000 字；纯函数）。
function cardSpeechText(post) {
  const title = String((post && post.title) || "").replace(/\s+/g, " ").trim();
  const summary = String((post && post.summary) || "").replace(/\s+/g, " ").trim();
  const text = title && summary ? title + "。" + summary : title || summary;
  return text.slice(0, 1000);
}

// fmt 秒 → mm:ss（纯函数）。
function fmt(sec) {
  if (!Number.isFinite(sec)) {
    return "0:00";
  }
  const m = Math.floor(sec / 60);
  const s = Math.floor(sec % 60);
  return m + ":" + String(s).padStart(2, "0");
}

// ---------- post.footer：详情页完整工具条 ----------

function registerFooter(ctx) {
  // 提取正文纯文本（压缩空白，供合成）
  const contentEl =
    document.querySelector(".rich-content") || document.querySelector(".md-body");
  const rawText = contentEl ? contentEl.innerText || "" : "";
  const text = rawText.replace(/\s+/g, " ").trim().slice(0, 20000);

  // 播放会话状态
  let audio = null; // 当前 <audio>
  let blobUrl = null; // objectURL（停止/结束/清理时 revoke）
  let busy = false; // 合成中标记

  // 工具条 DOM（沿用宿主 Tailwind 设计变量）
  const wrap = document.createElement("div");
  wrap.className = "my-4 flex flex-wrap items-center gap-2 rounded-lg border border-line bg-elevated px-4 py-3";
  wrap.setAttribute("data-tts-wrap", "");
  wrap.innerHTML =
    '<button type="button" data-tts-play class="flex items-center gap-1.5 rounded-full bg-accent px-4 py-1.5 text-xs font-medium text-on-accent hover:opacity-90">' +
    '<span aria-hidden>🔊</span><span data-tts-play-label>朗读</span></button>' +
    '<button type="button" data-tts-pause hidden class="rounded-full border border-line px-3 py-1.5 text-xs text-ink-2 hover:text-ink">⏸ 暂停</button>' +
    '<button type="button" data-tts-stop hidden class="rounded-full border border-line px-3 py-1.5 text-xs text-ink-2 hover:text-ink">⏹ 停止</button>' +
    '<label class="flex items-center gap-1 text-xs text-ink-3">倍速' +
    '<select data-tts-rate class="h-7 rounded-md border border-line bg-elevated px-1.5 text-xs text-ink">' +
    '<option value="0.75">0.75x</option><option value="1" selected>1x</option>' +
    '<option value="1.25">1.25x</option><option value="1.5">1.5x</option><option value="2">2x</option>' +
    "</select></label>" +
    '<span data-tts-progress class="text-xs tabular-nums text-ink-3"></span>' +
    '<span data-tts-status class="ml-auto text-xs text-ink-3"></span>';

  // 元素引用
  const playBtn = wrap.querySelector("[data-tts-play]");
  const playLabel = wrap.querySelector("[data-tts-play-label]");
  const pauseBtn = wrap.querySelector("[data-tts-pause]");
  const stopBtn = wrap.querySelector("[data-tts-stop]");
  const rateSelect = wrap.querySelector("[data-tts-rate]");
  const progressEl = wrap.querySelector("[data-tts-progress]");
  const statusEl = wrap.querySelector("[data-tts-status]");

  // setStatus 更新状态提示（纯 textContent，防注入）
  const setStatus = (msg) => {
    statusEl.textContent = msg;
  };

  // setLoading 切换合成中态
  const setLoading = (on) => {
    busy = on;
    playBtn.disabled = on;
    playLabel.textContent = on ? "合成中…" : "朗读";
    setStatus(on ? "正在合成语音，请稍候…" : "");
  };

  // stopAudio 停止并释放 Blob URL（保留工具条以便再次朗读）
  const stopAudio = () => {
    if (audio) {
      audio.pause();
      audio = null;
    }
    if (blobUrl) {
      URL.revokeObjectURL(blobUrl);
      blobUrl = null;
    }
    pauseBtn.hidden = true;
    stopBtn.hidden = true;
    progressEl.textContent = "";
    playLabel.textContent = "朗读";
  };

  // playBlob 播放合成音频
  const playBlob = (url) => {
    audio = new Audio(url);
    audio.playbackRate = Number(rateSelect.value) || 1;
    audio.addEventListener("timeupdate", () => {
      progressEl.textContent =
        fmt(audio.currentTime) + " / " + fmt(audio.duration);
    });
    audio.addEventListener("ended", () => {
      stopAudio();
      setStatus("朗读完成");
    });
    audio.addEventListener("error", () => {
      setStatus("播放失败，请重试");
      stopAudio();
    });
    audio.play().catch(() => {
      setStatus("自动播放被浏览器拦截，请点击播放重试");
    });
    pauseBtn.hidden = false;
    stopBtn.hidden = false;
    playLabel.textContent = "重播";
    setStatus("朗读中…");
  };

  // startSpeak 提取正文 → 合成 → 拉音频 → 播放
  const startSpeak = async () => {
    if (busy) {
      return;
    }
    if (!text) {
      setStatus("本文没有可朗读的正文");
      return;
    }
    stopAudio();
    setLoading(true);
    try {
      blobUrl = await fetchAudioBlob(text);
      setLoading(false);
      playBlob(blobUrl);
    } catch (err) {
      setLoading(false);
      setStatus(String(err && err.message ? err.message : err));
    }
  };

  // 事件绑定
  playBtn.addEventListener("click", () => startSpeak());
  pauseBtn.addEventListener("click", () => {
    if (audio) {
      if (audio.paused) {
        void audio.play();
        pauseBtn.textContent = "⏸ 暂停";
      } else {
        audio.pause();
        pauseBtn.textContent = "▶ 继续";
      }
    }
  });
  stopBtn.addEventListener("click", () => {
    stopAudio();
    setStatus("");
  });
  rateSelect.addEventListener("change", () => {
    if (audio) {
      audio.playbackRate = Number(rateSelect.value) || 1;
    }
  });

  // 挂载
  ctx.el.appendChild(wrap);

  // 清理函数（插件停用/卸载时调用：停音 + 释放资源）
  return () => {
    stopAudio();
    wrap.remove();
  };
}

// ---------- post.card.actions：时间线卡片迷你控件 ----------

function registerCardAction(ctx) {
  const text = cardSpeechText(ctx.props && ctx.props.post);

  // 播放会话状态
  let audio = null;
  let blobUrl = null;
  let busy = false;

  // 迷你控件：朗读按钮（多态：朗读/合成中/暂停-继续）+ 播放中出现的停止按钮
  const wrap = document.createElement("span");
  wrap.className = "flex items-center gap-2";
  wrap.setAttribute("data-tts-card", "");
  wrap.innerHTML =
    '<button type="button" data-tts-card-play class="flex items-center gap-1 transition-colors hover:text-ink">' +
    '<span aria-hidden>🔊</span><span data-tts-card-label>朗读</span></button>' +
    '<button type="button" data-tts-card-stop hidden class="flex items-center gap-1 transition-colors hover:text-ink">⏹</button>';

  const playBtn = wrap.querySelector("[data-tts-card-play]");
  const playLabel = wrap.querySelector("[data-tts-card-label]");
  const stopBtn = wrap.querySelector("[data-tts-card-stop]");

  // reset 复位为初始朗读按钮
  const reset = () => {
    if (audio) {
      audio.pause();
      audio = null;
    }
    if (blobUrl) {
      URL.revokeObjectURL(blobUrl);
      blobUrl = null;
    }
    stopBtn.hidden = true;
    playLabel.textContent = "朗读";
  };

  // toggle 播放/暂停切换（播放中按钮即暂停入口）
  const toggle = () => {
    if (busy || !text) {
      return;
    }
    if (audio) {
      if (audio.paused) {
        void audio.play();
        playLabel.textContent = "⏸";
      } else {
        audio.pause();
        playLabel.textContent = "▶";
      }
      return;
    }
    void start();
  };

  // start 合成 → 播放（按钮态：朗读 → 合成中… → ⏸）
  const start = async () => {
    busy = true;
    playBtn.disabled = true;
    playLabel.textContent = "合成中…";
    try {
      blobUrl = await fetchAudioBlob(text);
      audio = new Audio(blobUrl);
      audio.addEventListener("timeupdate", () => {
        if (audio) {
          playLabel.textContent = "⏸ " + fmt(audio.currentTime);
        }
      });
      audio.addEventListener("ended", reset);
      audio.addEventListener("error", reset);
      audio.play().catch(() => {
        playLabel.textContent = "▶ 点击播放";
      });
      playBtn.disabled = false;
      busy = false;
      stopBtn.hidden = false;
      playLabel.textContent = "⏸";
    } catch (err) {
      // 失败回退朗读态并短暂提示原因（title 提示不打断浏览）
      reset();
      playBtn.disabled = false;
      busy = false;
      playBtn.title = String(err && err.message ? err.message : err);
    }
  };

  playBtn.addEventListener("click", toggle);
  stopBtn.addEventListener("click", reset);

  // 无可朗读文本：不挂载按钮（纯图片帖等场景保持操作条干净）
  if (!text) {
    return () => undefined;
  }
  ctx.el.appendChild(wrap);

  // 清理函数（停音 + 释放资源 + 移除控件）
  return () => {
    reset();
    wrap.remove();
  };
}
