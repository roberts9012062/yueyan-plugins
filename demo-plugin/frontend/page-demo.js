// cmd/demo-plugin/frontend/page-demo.js
// 演示插件独立页面（M3.9 admin.page 能力）：
//   壳路由 /admin/plugin-pages/demo-plugin/demo 加载本模块（registerPage 契约）。
// ctx: { container, api(受限 API 客户端), user(脱敏用户), params: {pluginId, page} }
export default function registerPage(ctx) {
  const box = document.createElement("div");
  box.className = "demo-plugin-card";
  box.style.padding = "16px";
  box.innerHTML =
    "<p><strong>演示插件独立页面</strong>（admin.page 能力 · M3.9）</p>" +
    "<p>当前用户：" + (ctx.user ? ctx.user.name + "（" + ctx.user.role + "）" : "未登录") + "</p>" +
    "<p>页面参数：" + JSON.stringify(ctx.params) + "</p>" +
    '<p><button type="button" data-demo-fetch>查询插件状态</button></p>' +
    '<p data-demo-result>（点击按钮调用插件 API）</p>';
  ctx.container.appendChild(box);

  // 调用插件自定义 API（/ping）演示受限 API 客户端
  box.querySelector("[data-demo-fetch]").addEventListener("click", async () => {
    const result = document.createElement("p");
    result.textContent = "加载中…";
    try {
      const data = await ctx.api.get("/ping");
      result.textContent = "插件 API 返回：" + JSON.stringify(data);
    } catch (err) {
      result.textContent = "调用失败：" + String(err);
    }
    box.querySelector("[data-demo-result]").replaceWith(result);
  });

  return () => {
    box.remove();
  };
}
