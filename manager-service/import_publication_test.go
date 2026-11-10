package main

import (
	"errors"
	"os"
	"path"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestInterruptedImportReconcilesOnlyCompletedPublication(t *testing.T) {
	for _, state := range []string{"published", "reserved", "replaced"} {
		t.Run(state, func(t *testing.T) {
			home := t.TempDir()
			a, err := newApp(home)
			if err != nil {
				t.Fatal(err)
			}
			b := addBatch(t, a, "Interrupted", 88)
			source := b.Files[0]
			op, err := a.begin("import", "Interrupted")
			if err != nil {
				t.Fatal(err)
			}
			b.State = "importing"
			const dest = "Artist/Album/Imported.flac"
			stop := errors.New("simulated interruption")
			prepare := func(result Track, marker string) error {
				b.PendingImport = &importPublication{SourceID: source.ID, OperationID: op.ID, Reservation: marker, Result: result}
				if err := a.put("batches", b.ID, b); err != nil {
					return err
				}
				if state == "reserved" {
					return stop
				}
				return nil
			}
			result, err := a.publishImport(source, source.Tags, nil, dest, false, prepare)
			if state == "reserved" {
				if !errors.Is(err, stop) {
					t.Fatal(err)
				}
				if _, err := a.music.Stat(path.Dir(dest)); !os.IsNotExist(err) {
					t.Fatalf("failed preparation left an empty target directory: %v", err)
				}
				if err := ensureDir(a.music, path.Dir(dest)); err != nil {
					t.Fatal(err)
				}
				writeImportFixture(t, a.music, dest, []byte(b.PendingImport.Reservation))
			} else if err != nil {
				t.Fatal(err)
			} else if state == "replaced" {
				writeImportFixture(t, a.music, dest, toneFLAC("External edit", 93))
			}
			a.close()
			a, err = newApp(home)
			if err != nil {
				t.Fatal(err)
			}
			defer a.close()
			stored, err := get[Batch](a, "batches", b.ID)
			if err != nil || stored.PendingImport != nil {
				t.Fatalf("publication not reconciled: %v %+v", err, stored.PendingImport)
			}
			if state == "published" {
				tracks := a.tracks()
				if len(stored.Files) != 0 || stored.State != "imported" || len(tracks) != 1 || tracks[0].ID != result.ID || latestOperation(t, a).Affected != 1 {
					t.Fatalf("published result was not committed exactly once: %+v %+v", stored, tracks)
				}
				if err := a.cleanupBatchLocked(&stored); err != nil {
					t.Fatal(err)
				}
				if _, err := a.data.Stat(source.Path); !os.IsNotExist(err) {
					t.Fatalf("committed source not cleaned: %v", err)
				}
			} else {
				if len(stored.Files) != 1 || stored.State != "ready" || len(a.tracks()) != 0 {
					t.Fatalf("incomplete or externally changed publication consumed draft: %+v", stored)
				}
				if hash, err := a.operationHash(a.data, source.Path); err != nil || hash != source.Hash {
					t.Fatalf("draft source changed or lost: %v", err)
				}
				if state == "reserved" {
					if _, err := a.music.Stat(dest); !os.IsNotExist(err) {
						t.Fatalf("reservation not reclaimed: %v", err)
					}
				} else if actual, err := inspect(a.music, dest); err != nil || actual.Tags.Title != "External edit" {
					t.Fatalf("external file was modified: %v %+v", err, actual)
				}
			}
		})
	}
}

func writeImportFixture(t *testing.T, root *os.Root, name string, data []byte) {
	t.Helper()
	if err := root.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
}

func TestImportFilenameFitsWithLongUnicodeTagsAndCollisionSuffix(t *testing.T) {
	name := destinationFilename(Track{Path: "sample.FLAC"}, Tags{
		Track: strings.Repeat("序", 100), Disc: "123456", Title: strings.Repeat("音", 100),
	})
	if !utf8.ValidString(name) || len(name)+len(" (1000)") > 255 || !strings.HasSuffix(name, ".flac") {
		t.Fatalf("invalid import filename: %d bytes, %q", len(name), name)
	}
}

func TestSkipCannotConsumeDraftForDifferentExplicitEdition(t *testing.T) {
	a := testApp(t)
	fixtureAlbumTrack(t, a, "Editions/First/old.flac", "2001", 81)
	fixtureAlbumTrack(t, a, "Editions/Second/other.flac", "2010", 82)
	b := addBatch(t, a, "Reissued recording", 81)
	const destination = "Editions/Second"
	key := albumPlacementKey(albumIdentity("Shared Album", "Alice & Bob"), destination)
	saveAlbumDraft(t, a, b, "2010", key)
	ids := fileIDs(b.Files)
	if err := a.importWithPolicy(b.ID, "skip", ids); err == nil || !strings.Contains(err.Error(), "保留副本") {
		t.Fatalf("skip silently discarded a recording for a different edition: %v", err)
	}
	if _, err := a.data.Stat(b.Files[0].Path); err != nil || len(a.tracks()) != 2 {
		t.Fatalf("rejected import mutated files: %v", err)
	}
	if err := a.importWithPolicy(b.ID, "keep", ids); err != nil {
		t.Fatal(err)
	}
	if len(a.tracks()) != 3 {
		t.Fatal("keeping the cross-edition recording did not publish it")
	}
	b = addBatch(t, a, "Repeated recording", 81)
	saveAlbumDraft(t, a, b, "2010", key)
	if err := a.importWithPolicy(b.ID, "skip", fileIDs(b.Files)); err != nil || len(a.tracks()) != 3 {
		t.Fatalf("existing duplicate in the chosen edition was not accepted: %v", err)
	}
}

func TestSkipInChosenMultiDiscAlbumDoesNotRequireDiscNumber(t *testing.T) {
	a := testApp(t)
	fixtureAlbumTrack(t, a, "Album/CD1/a.flac", "2001", 84)
	fixtureAlbumTrack(t, a, "Album/CD2/b.flac", "2001", 85)
	b := addBatch(t, a, "Recording without disc", 84)
	key := albumPlacementKey(albumIdentity("Shared Album", "Alice & Bob"), "Album")
	saveAlbumDraft(t, a, b, "2001", key)
	b, err := get[Batch](a, "batches", b.ID)
	if err != nil {
		t.Fatal(err)
	}
	b.Candidates[0].Tags[0].Disc = ""
	if err := a.put("batches", b.ID, b); err != nil {
		t.Fatal(err)
	}
	if err := a.importWithPolicy(b.ID, "skip", fileIDs(b.Files)); err != nil || len(a.tracks()) != 2 {
		t.Fatalf("skip attempted to allocate an unnecessary disc destination: %v", err)
	}
}
