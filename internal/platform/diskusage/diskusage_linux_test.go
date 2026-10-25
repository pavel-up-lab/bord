//go:build linux

package diskusage

import "testing"

func TestUsageReportsBoundedCapacity(t *testing.T) {
	space, err := Usage(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if space.TotalBytes <= 0 || space.AvailableBytes < 0 || space.AvailableBytes > space.TotalBytes {
		t.Fatalf("space=%+v", space)
	}
}
