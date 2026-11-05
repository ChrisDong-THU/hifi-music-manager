package main

import (
	"errors"
	"io"
	"net/http"
	"net/url"
)

// Image forms share one transport boundary: bounded body, complete image
// validation and unconditional release of multipart spill files. Business
// handlers only interpret the returned text fields and optional artwork.
func decodeImageForm(w http.ResponseWriter, r *http.Request, imageField string, textBudget int64) (url.Values, *albumCover, error) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAlbumCover+textBudget)
	err := r.ParseMultipartForm(1 << 20)
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	if err != nil {
		return nil, nil, errors.New("提交数据无效或图片超过 10 MiB")
	}
	file, _, err := r.FormFile(imageField)
	if err == http.ErrMissingFile {
		return r.MultipartForm.Value, nil, nil
	}
	if err != nil {
		return nil, nil, errors.New("图片上传无效")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxAlbumCover+1))
	if err != nil {
		return nil, nil, errors.New("图片读取失败")
	}
	cover, err := parseAlbumCover(data)
	return r.MultipartForm.Value, cover, err
}
