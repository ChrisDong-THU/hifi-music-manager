package main

import "errors"

func (a *app) checkDraftWritable(b Batch) error {
	if err := draftWritable(b); err != nil {
		return err
	}
	return a.checkOperation()
}

func draftWritable(b Batch) error {
	if b.PendingImport != nil {
		return errors.New("上次导入的文件尚在核验，请稍后重试")
	}
	if b.State != "ready" && b.State != "uploading" {
		return errors.New("当前草稿不能继续整理")
	}
	if len(b.Cleanup) > 0 {
		return errors.New("上传源文件清理尚未完成，请稍后重试")
	}
	return nil
}
