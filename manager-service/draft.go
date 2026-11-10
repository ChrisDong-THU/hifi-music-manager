package main

// File tags already live in Files. Only a valid saved edit needs a second
// representation; existing original candidates remain readable but are unused.
func savedDraftTags(b Batch) []Tags {
	for _, c := range b.Candidates {
		if c.ID == b.ChosenCandidate && c.Source == "manual" && len(c.Tags) == len(b.Files) {
			return c.Tags
		}
	}
	return nil
}

func (a *app) localPreview(id string) (resultErr error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	defer func() { resultErr = a.failDraftStages(id, "preview", resultErr) }()
	b, err := get[Batch](a, "batches", id)
	if err != nil {
		return err
	}
	if err := a.checkDraftWritable(b); err != nil {
		return err
	}
	b.ReadState, b.ReadError = "running", ""
	if err := a.put("batches", b.ID, b); err != nil {
		return err
	}
	for i, previous := range b.Files {
		current, err := inspectContext(a.operationContext(), a.data, previous.Path)
		if err != nil {
			return err
		}
		current.ID, current.Added = previous.ID, previous.Added
		b.Files[i] = current
	}
	b.Quality = qualityForFiles(b.Quality, b.Files)
	if len(b.Quality) != len(b.Files) {
		b.QualityState, b.QualityError = "queued", ""
	}
	b.Candidates = nil
	b.AlbumMatch = ""
	b.Previewed = true
	b.ReadState, b.ReadError = "done", ""
	b.ChosenCandidate = "original"
	b.State = "ready"
	b.Message = "已读取文件标签，等待导入"
	return a.putDraft(b, nil, true)
}
