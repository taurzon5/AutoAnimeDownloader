package files

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// A release is recorded once per hash: adding the same torrent again (a pack, once per episode; a
// redownload) keeps the first record and only adds the episodes.
func TestReleaseStoreRecordKeepsTheFirstRecord(t *testing.T) {
	s := NewReleaseStore(filepath.Join(t.TempDir(), "releases.json"))
	if all, err := s.All(); err != nil || len(all) != 0 {
		t.Fatalf("a missing file must read as empty: %v %v", all, err)
	}
	first := ReleaseRecord{Name: "[X] Show 01-12 [1080p]", Origin: ReleaseOriginSearch, AddedAt: time.Date(2026, 11, 24, 0, 0, 0, 0, time.UTC)}
	if err := s.Record("aaa", first, 7, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("aaa", ReleaseRecord{Name: "other", Origin: ReleaseOriginMagnet}, 7, 1, 3); err != nil {
		t.Fatal(err)
	}
	if err := s.Record("aaa", ReleaseRecord{}, 9, 2); err != nil {
		t.Fatal(err)
	}
	all, err := s.All()
	if err != nil {
		t.Fatal(err)
	}
	got := all["aaa"]
	if got.Name != first.Name || got.Origin != ReleaseOriginSearch || !got.AddedAt.Equal(first.AddedAt) {
		t.Errorf("the first record must stay: %+v", got)
	}
	if want := map[int][]int{7: {1, 3}, 9: {2}}; !reflect.DeepEqual(got.Episodes, want) {
		t.Errorf("episodes %v, want %v", got.Episodes, want)
	}
}

// A nil store (the daemon never set one) records nothing and reads empty.
func TestReleaseStoreNil(t *testing.T) {
	var s *ReleaseStore
	if err := s.Record("aaa", ReleaseRecord{}, 1, 1); err != nil {
		t.Fatal(err)
	}
	if all, err := s.All(); err != nil || len(all) != 0 {
		t.Fatalf("%v %v", all, err)
	}
}
