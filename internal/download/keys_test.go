package download

import (
	"reflect"
	"testing"

	"github.com/xiiisins/md-downloader/internal/mangadex"
)

func TestChapterKeys(t *testing.T) {
	chs := []mangadex.Chapter{
		{Volume: "1", Number: "1"}, {Volume: "1", Number: "5.5"}, {Volume: "2", Number: "1"}, // same file name as the first
		{Number: "oneshot"}, {Number: "oneshot"}, {Number: "12"}, {Number: "10000"},
	}
	want := []string{"0001", "0005.5", "oneshot", "0012", "10000"}
	if got := ChapterKeys(chs); !reflect.DeepEqual(got, want) {
		t.Errorf("ChapterKeys = %v, want %v", got, want)
	}
}

func TestChapterKeyMatchesTheFileName(t *testing.T) {
	// The scanner finds files by "ch"+key+".cbz"; SyncManga must write the same names.
	if got := "ch" + ChapterKey("7") + ".cbz"; got != "ch0007.cbz" {
		t.Errorf("file name = %q", got)
	}
}
