// cmd/demo-plugin/frontend/index.js
// 演示插件前端扩展（M3.6）：订阅 post.footer 槽位。
// 契约（docs/plugin-dev-guide.md 8.1）：默认导出 register(ctx)，返回清理函数。
// ctx: { slot, el(挂载点 DOM), api(受限 API 客户端), user(用户信息) }
export default function register(ctx) {
  // 渲染内容（演示：插件标识 + 功能状态占位）
  const wrapper = document.createElement("div");
  wrapper.className = "demo-plugin-footer";
  wrapper.innerHTML =
    '<div class="demo-plugin-card">' +
    '<span class="demo-plugin-badge">演示插件</span>' +
    "<span>文章页脚扩展（M3.6 前端扩展点）</span>" +
    "</div>";
  ctx.el.appendChild(wrapper);

  // 清理函数（组件卸载/插件停用时调用）
  return () => {
    wrapper.remove();
  };
}
