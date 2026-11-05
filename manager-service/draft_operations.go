package main

import "errors"

func (a *app) checkDraftWritable(id string) error {
	b, err := get[Batch](a, "batches", id)
	if err != nil {
		return err
	}
	if b.State != "ready" && b.State != "uploading" {
		return errors.New("当前草稿不能继续整理")
	}
	if len(b.Cleanup) > 0 {
		return errors.New("上传源文件清理尚未完成，请稍后重试")
	}
	return a.checkOperation()
}
