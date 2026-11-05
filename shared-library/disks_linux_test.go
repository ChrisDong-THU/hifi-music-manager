//go:build linux

package library

import (
	"testing"
)

func TestDesktopUSBDiscoveryUsesRealMountsAndUSBTransport(t *testing.T) {
	mounts := parseMountTable([]byte("31 1 8:1 / /media/jetson/Music\\040Disk rw,nosuid shared:1 - fuseblk /dev/sda1 rw,user_id=0\n32 1 8:2 / /media/jetson/RO ro - vfat /dev/sda2 ro\n33 1 179:1 / / rw - ext4 /dev/mmcblk0p1 rw\n"))
	data := []byte(`{"blockdevices":[{"name":"/dev/sda","type":"disk","tran":"usb","children":[{"name":"/dev/sda1","type":"part","fstype":"exfat","mountpoint":"/media/jetson/Music Disk"},{"name":"/dev/sda2","type":"part","fstype":"vfat","mountpoint":"/media/jetson/RO"},{"name":"/dev/sda3","type":"part","fstype":"exfat","mountpoint":"/media/jetson/stale"}]},{"name":"/dev/mmcblk0p1","type":"part","tran":null,"mountpoint":"/"}]}`)
	drives, err := parseUSBDrives(data, mounts)
	if err != nil || len(drives) != 2 {
		t.Fatal("wrong USB discovery", drives, err)
	}
	if drives[0].Root != "/media/jetson/Music Disk" || drives[0].MountID != "31" || !drives[0].Writable || drives[1].Writable {
		t.Fatal("mount parsing lost path, generation or read-only state")
	}
}
