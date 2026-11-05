import { CircleAlert } from "lucide-react";
import type { Notice } from "./notice-state";
import "./notice-status-icon.css";

export function NoticeStatusIcon({ status }: { status: Notice["status"] }) {
  if (status === "error")
    return <CircleAlert className="notice-icon" size={20} aria-hidden="true" />;
  return (
    <svg
      className="notice-icon notice-status-icon"
      data-status={status}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <circle
        className="notice-icon-ring"
        cx="12"
        cy="12"
        r="9"
        pathLength="100"
      />
      <g className="notice-icon-wave">
        <path d="M2 10v3" />
        <path d="M6 6v11" />
        <path d="M10 3v18" />
        <path d="M14 8v7" />
        <path d="M18 5v13" />
        <path d="M22 10v3" />
      </g>
      <path
        className="notice-icon-check"
        d="m8 12 2.5 2.5L16 9"
        pathLength="1"
      />
    </svg>
  );
}
