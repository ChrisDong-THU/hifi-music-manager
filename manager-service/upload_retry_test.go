package main

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

func TestUploadRetryPreservesIdentityAndRejectsChangedContent(t *testing.T) {
	a := testApp(t)
	w := request(a, "POST", "/api/drafts", `{"name":"Retry"}`, true)
	var batch Batch
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &batch) != nil {
		t.Fatal(w.Body.String())
	}
	path := "/api/drafts/" + batch.ID + "/files?path=retry.flac"
	body := string(toneFLAC("retry", 7))
	first := request(a, "POST", path, body, true)
	retry := request(a, "POST", path, body, true)
	if first.Code != 200 || retry.Code != 200 || first.Body.String() != retry.Body.String() {
		t.Fatalf("retry must return the original track: %d %d", first.Code, retry.Code)
	}
	batch, err := get[Batch](a, "batches", batch.ID)
	if err != nil || len(batch.Files) != 1 {
		t.Fatalf("retry duplicated the entry: %+v %v", batch.Files, err)
	}
	if changed := request(a, "POST", path, string(toneFLAC("different", 8)), true); changed.Code != 409 {
		t.Fatalf("changed retry accepted: %d", changed.Code)
	}
	if hash, err := fileHash(a.data, batch.Files[0].Path); err != nil || hash != batch.Files[0].Hash {
		t.Fatal("conflict changed the uploaded file", err)
	}
	// Repeating the last response is also safe at the batch size limit.
	for i := 1; i < 300; i++ {
		batch.Files = append(batch.Files, Track{ID: fmt.Sprint(i), Path: fmt.Sprintf("uploads/%s/%d.flac", batch.ID, i)})
	}
	if err := a.put("batches", batch.ID, batch); err != nil {
		t.Fatal(err)
	}
	if retry = request(a, "POST", path, body, true); retry.Code != 200 {
		t.Fatalf("full batch rejected its last response retry: %d", retry.Code)
	}
	if fresh := request(a, "POST", "/api/drafts/"+batch.ID+"/files?path=new.flac", body, true); fresh.Code != 400 {
		t.Fatalf("batch limit bypassed: %d", fresh.Code)
	}
	// A stale DB record must not hide an externally modified file.
	f, err := a.data.OpenFile(batch.Files[0].Path, os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString("external change")
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if changed := request(a, "POST", path, body, true); changed.Code != 409 {
		t.Fatalf("externally modified file accepted: %d", changed.Code)
	}
}
