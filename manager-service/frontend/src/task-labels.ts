const operations: Record<string, string> = {
  scan: "扫描曲库",
  sync: "同步曲库",
  import: "导入音乐",
  edit: "保存标签",
  delete: "删除音乐",
  "draft-delete": "删除草稿",
  disks: "设置数据盘",
};
export function taskOperation(label: string) {
  return label.replace(/^(正在|完成)/, "").trim();
}
export function taskMessage(operation: string, status: "active" | "success") {
  return (status === "active" ? "正在" : "完成") + taskOperation(operation);
}
export const taskLabels: Record<string, string> = Object.fromEntries(
  Object.entries(operations).map(([kind, operation]) => [
    kind,
    taskMessage(operation, "active"),
  ]),
);
export const completedLabels: Record<string, string> = Object.fromEntries(
  Object.entries(operations).map(([kind, operation]) => [
    kind,
    taskMessage(operation, "success"),
  ]),
);

export function progressCountSpace(count: string) {
  return count.replace(/\d+\/(\d+)/g, "$1/$1").replace(/\d+%/g, "100%");
}
export function taskKind(path: string) {
  if (path === "scan") return "scan";
  const action = path.split("/").pop() || "";
  if (path.startsWith("drafts/") && ["delete", "discard"].includes(action))
    return "draft-delete";
  return action;
}
