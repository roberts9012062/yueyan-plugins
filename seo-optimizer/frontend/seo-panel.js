// cmd/seo-plugin/frontend/seo-panel.js
// SEO 优化插件 · 发帖 SEO 面板（M4.1 compose.seo 槽位）：
//   渲染 SEO 标题（12/60 字数）/描述（18/160）/URL 别名/收录开关/OG 提示/重置，
//   值经 ctx.props.onChange 回写发帖表单（随发帖请求提交，主进程写入 seo_meta）。
// ctx: { slot, el, api, user, props: { initial, onChange } }
// 说明：props 为挂载时快照（PluginSlot 约定）；onChange 稳定回调（React setState）。
export default function register(ctx) {
  const initial = ctx.props?.initial || {};
  // 面板内部状态（不回读外部——避免 props 变化重挂载导致输入失焦）
  const state = {
    seo_title: initial.seo_title || "",
    seo_description: initial.seo_description || "",
    url_alias: initial.url_alias || "",
    robots: initial.robots || "",
  };

  const wrapper = document.createElement("div");
  wrapper.className = "mt-4 rounded-lg border border-line bg-elevated p-4";

  const emit = () => {
    if (typeof ctx.props?.onChange === "function") {
      ctx.props.onChange({ ...state });
    }
  };

  const count = (text, max) => {
    const n = Array.from(text).length;
    return `${Math.min(n, max)}/${max}`;
  };

  wrapper.innerHTML =
    '<div class="flex items-center justify-between">' +
    "<p class='text-sm font-medium text-ink'>SEO</p>" +
    "<p class='text-[10px] text-ink-3'>SEO 插件已启用 · 可自定义收录</p>" +
    "</div>" +
    // AI 辅助区（M4.1：模型选择 + 生成按钮；无配置时提示跳转 /admin/ai）
    '<div class="mt-2 rounded-md bg-muted px-3 py-2" data-ai-zone>' +
    '<div class="flex items-center gap-2">' +
    '<span class="text-xs text-ink-3">AI 辅助</span>' +
    '<select data-ai-model class="h-7 min-w-0 flex-1 rounded-md border border-line bg-elevated px-2 text-xs text-ink focus:border-accent focus:outline-none" disabled>' +
    '<option value="">（未配置 AI）</option>' +
    "</select>" +
    "</div>" +
    '<div class="mt-1.5 flex items-center gap-2" data-ai-actions hidden>' +
    '<button type="button" data-ai-title class="rounded-full bg-accent-soft px-3 py-1 text-xs text-glow hover:opacity-90">✨ 生成标题</button>' +
    '<button type="button" data-ai-desc class="rounded-full bg-accent-soft px-3 py-1 text-xs text-glow hover:opacity-90">✨ 生成描述</button>' +
    '<span class="text-[10px] text-ink-3" data-ai-status></span>' +
    "</div>" +
    '<p class="mt-1 text-[10px] text-ink-3" data-ai-hint></p>' +
    "</div>" +
    '<div class="mt-3 space-y-3">' +
    // SEO 标题
    '<div><label class="mb-1 block text-xs text-ink-3">SEO 标题（可选，默认用正文摘要）</label>' +
    '<input data-seo-title type="text" class="h-9 w-full rounded-lg border border-line bg-muted px-3 text-sm text-ink focus:border-accent focus:outline-none" placeholder="月光落在窗台…">' +
    '<p class="mt-0.5 text-right text-[10px] text-ink-3" data-seo-title-count></p></div>' +
    // SEO 描述
    '<div><label class="mb-1 block text-xs text-ink-3">SEO 描述</label>' +
    '<textarea data-seo-desc rows="2" class="w-full resize-none rounded-lg border border-line bg-muted px-3 py-2 text-sm text-ink focus:border-accent focus:outline-none" placeholder="月光落在窗台，像一封还没写完的信。"></textarea>' +
    '<p class="mt-0.5 text-right text-[10px] text-ink-3" data-seo-desc-count></p></div>' +
    // URL 别名 + 收录
    '<div class="flex items-end gap-3">' +
    '<div class="flex-1"><label class="mb-1 block text-xs text-ink-3">URL 别名</label>' +
    '<div class="flex items-center gap-1.5"><span class="text-xs text-ink-3">/p/</span>' +
    '<input data-seo-alias type="text" class="h-9 w-full rounded-lg border border-line bg-muted px-3 text-sm text-ink focus:border-accent focus:outline-none" placeholder="silver-window"></div></div>' +
    '<div class="w-36"><label class="mb-1 block text-xs text-ink-3">收录</label>' +
    '<select data-seo-robots class="h-9 w-full rounded-lg border border-line bg-muted px-2 text-sm text-ink focus:border-accent focus:outline-none">' +
    '<option value="">开启（index, follow）</option>' +
    '<option value="noindex, nofollow">关闭（noindex, nofollow）</option>' +
    "</select></div></div>" +
    '<p class="text-[10px] text-ink-3">OG 图片：使用帖子封面（默认）</p>' +
    // 操作
    '<div class="flex items-center justify-between pt-1">' +
    '<p class="text-[10px] text-ink-3" data-seo-hint>可覆盖全局默认</p>' +
    '<div class="flex gap-2">' +
    '<button type="button" data-seo-reset class="rounded-full border border-line px-3 py-1 text-xs text-ink-2 hover:text-ink">重置</button>' +
    '<button type="button" data-seo-apply class="rounded-full bg-accent px-4 py-1 text-xs text-on-accent hover:opacity-90">应用并返回</button>' +
    "</div></div></div>";

  // 绑定输入（每次输入更新内部状态 + 字数统计 + 回写）
  const titleInput = wrapper.querySelector("[data-seo-title]");
  const descInput = wrapper.querySelector("[data-seo-desc]");
  const aliasInput = wrapper.querySelector("[data-seo-alias]");
  const robotsSelect = wrapper.querySelector("[data-seo-robots]");
  const titleCount = wrapper.querySelector("[data-seo-title-count]");
  const descCount = wrapper.querySelector("[data-seo-desc-count]");
  const hint = wrapper.querySelector("[data-seo-hint]");

  titleInput.value = state.seo_title;
  descInput.value = state.seo_description;
  aliasInput.value = state.url_alias;
  robotsSelect.value = state.robots;
  titleCount.textContent = count(state.seo_title, 60);
  descCount.textContent = count(state.seo_description, 160);

  // ---------- AI 辅助（M4.1）：模型列表 → 有模型可选可生成；无模型提示跳转 ----------
  const aiZone = wrapper.querySelector("[data-ai-zone]");
  const aiModel = wrapper.querySelector("[data-ai-model]");
  const aiActions = wrapper.querySelector("[data-ai-actions]");
  const aiHint = wrapper.querySelector("[data-ai-hint]");
  const aiStatus = wrapper.querySelector("[data-ai-status]");
  let models = []; // [{name, models: []}]
  const prompts = {
    title: "你是一名 SEO 专家。请为以下内容生成一个简洁有吸引力的 SEO 标题（不超过 30 字，不要引号）。内容：",
    desc: "你是一名 SEO 专家。请为以下内容生成一段 SEO 描述（不超过 60 字，不要引号）。内容：",
  };
  // 加载模型（经插件 API 代理 → 数据服务）
  ctx.api
    .get("/ai/models")
    .then((r) => {
      models = r.models || [];
      const configured = (r.configured ?? models.length > 0) && models.length > 0;
      if (!configured) {
        // 无 AI 配置：提示 + 跳转链接（管理员前往配置；普通用户提示联系）
        aiHint.innerHTML =
          "未配置 AI 供应商，AI 辅助生成不可用。" +
          '<a href="/admin/ai" class="text-glow underline">去配置 AI</a>' +
          "（需管理员权限）";
        return;
      }
      // 有模型：填充下拉（供应商 · 模型）
      aiModel.disabled = false;
      const opts = [];
      for (const m of models) {
        for (const model of m.models || []) {
          opts.push(`<option value="${model}">${m.name} · ${model}</option>`);
        }
      }
      aiModel.innerHTML = '<option value="">选择模型…</option>' + opts.join("");
      aiActions.hidden = false;
      aiHint.textContent = "选择模型后可 AI 生成 SEO 标题/描述";
    })
    .catch(() => {
      aiHint.textContent = "AI 服务暂不可用";
    });
  // AI 生成（标题/描述；内容源 = 面板已有内容——插件与发帖表单隔离，拿不到页面正文）
  const generate = (kind) => {
    const model = aiModel.value;
    if (!model) {
      aiStatus.textContent = "请先选择模型";
      return;
    }
    const source = state.seo_title || state.seo_description || aliasInput.value;
    if (!source) {
      aiStatus.textContent = "请先填写 SEO 标题或描述（AI 基于面板内容生成）";
      return;
    }
    aiStatus.textContent = "生成中…";
    ctx.api
      .post("/ai/generate", { model, prompt: prompts[kind], content: source })
      .then((r) => {
        if (r.error) {
          aiStatus.textContent = r.error;
          return;
        }
        const text = (r.text || "").trim();
        if (kind === "title") {
          state.seo_title = text;
          titleInput.value = text;
          titleCount.textContent = count(text, 60);
        } else {
          state.seo_description = text;
          descInput.value = text;
          descCount.textContent = count(text, 160);
        }
        aiStatus.textContent = "已生成";
        emit();
      })
      .catch(() => {
        aiStatus.textContent = "生成失败，请稍后再试";
      });
  };
  wrapper.querySelector("[data-ai-title]").addEventListener("click", () => generate("title"));
  wrapper.querySelector("[data-ai-desc]").addEventListener("click", () => generate("desc"));

  titleInput.addEventListener("input", () => {
    state.seo_title = titleInput.value;
    titleCount.textContent = count(state.seo_title, 60);
  });
  descInput.addEventListener("input", () => {
    state.seo_description = descInput.value;
    descCount.textContent = count(state.seo_description, 160);
  });
  aliasInput.addEventListener("input", () => {
    state.url_alias = aliasInput.value;
  });
  robotsSelect.addEventListener("change", () => {
    state.robots = robotsSelect.value;
  });

  // 重置：清空面板与发帖表单（onChange 回写空）
  wrapper.querySelector("[data-seo-reset]").addEventListener("click", () => {
    state.seo_title = "";
    state.seo_description = "";
    state.url_alias = "";
    state.robots = "";
    titleInput.value = "";
    descInput.value = "";
    aliasInput.value = "";
    robotsSelect.value = "";
    titleCount.textContent = "0/60";
    descCount.textContent = "0/160";
    emit();
    hint.textContent = "已重置为默认";
  });

  // 应用并返回：回写并收起面板（下次展开回填当前值）
  wrapper.querySelector("[data-seo-apply]").addEventListener("click", () => {
    emit();
    wrapper.querySelector(".space-y-3").style.display = "none";
    hint.textContent = "已应用（可重新展开编辑）";
    wrapper.querySelector("[data-seo-title]").dispatchEvent(new Event("focus"));
  });

  ctx.el.appendChild(wrapper);

  return () => {
    wrapper.remove();
  };
}
