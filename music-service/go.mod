module hifi.local/music-service

go 1.25.0

require hifi.local/library v0.0.0

require golang.org/x/sys v0.45.0 // indirect

replace hifi.local/library => ../shared-library
