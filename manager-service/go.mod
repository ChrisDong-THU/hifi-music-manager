module hifi.local/manager-service

go 1.25.0

require (
	github.com/bogem/id3v2/v2 v2.1.4
	github.com/dhowden/tag v0.0.0-20240417053706-3d75831295e8
	github.com/mewkiz/flac v1.0.14
	go.etcd.io/bbolt v1.5.0
	hifi.local/library v0.0.0
)

replace hifi.local/library => ../shared-library

require (
	github.com/icza/bitio v1.1.0 // indirect
	github.com/mewkiz/pkg v0.0.0-20250417130911-3f050ff8c56d // indirect
	github.com/mewpkg/term v0.0.0-20241026122259-37a80af23985 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)
