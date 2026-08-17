// cmd/demo-plugin/frontend/comment-item.js
// 演示插件 comment.item 扩展（M3.9 每条评论独立槽位）：
//   在每条顶层评论下方渲染一个标识卡片（props.comment 透传评论对象）。
// 契约：默认导出 register(ctx)，返回清理函数。
// ctx: { slot, el, api, user, props: { comment } }
export default function register(ctx) {
  const comment = ctx.props?.comment;
  const wrapper = document.createElement("div");
  wrapper.className = "demo-plugin-comment-item";
  wrapper.innerHTML =
    '<div class="demo-plugin-card" style="margin:4px 0">' +
    '<span class="demo-plugin-badge">演示插件</span>' +
    "<span>评论扩展 · 楼层评论 ID " + (comment?.id ?? "?") + "</span>" +
    "</div>";
  ctx.el.appendChild(wrapper);

  return () => {
    wrapper.remove();
  };
}
