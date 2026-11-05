import { Button } from "@mui/material";
import type { ReactNode } from "react";
import { Confirm } from "./components";

export function DeleteMusicButton({
  title,
  description,
  disabled,
  onDelete,
  startIcon,
  confirmColor = "primary",
}: {
  title: string;
  description: string;
  disabled: boolean;
  onDelete: () => Promise<void>;
  startIcon?: ReactNode;
  confirmColor?: "primary" | "secondary" | "error";
}) {
  return (
    <Confirm
      title={title}
      text={description}
      color={confirmColor}
      onConfirm={onDelete}
    >
      {(open) => (
        <Button
          color="primary"
          variant="outlined"
          startIcon={startIcon}
          disabled={disabled}
          onClick={open}
          sx={{ mr: "auto", borderColor: "primary.main", flexShrink: 0 }}
        >
          删除
        </Button>
      )}
    </Confirm>
  );
}
