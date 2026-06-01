package sophon

import "testing"

const branchesMainPredl = `{
  "game_branches": [{
    "game": {"id": "gopR6Cufr3", "biz": "hk4e_global"},
    "main": {
      "package_id": "ScSYQBFhu9", "branch": "main", "password": "pw1",
      "tag": "6.6.0", "diff_tags": ["6.5.0", "6.4.0"],
      "categories": [
        {"category_id": "10016", "matching_field": "game",  "type": "CATEGORY_TYPE_RESOURCE"},
        {"category_id": "10018", "matching_field": "en-us", "type": "CATEGORY_TYPE_AUDIO"}
      ]
    },
    "pre_download": {
      "package_id": "PreDL01", "branch": "predownload", "password": "pw2",
      "tag": "6.7.0", "diff_tags": ["6.6.0"],
      "categories": [
        {"category_id": "10016", "matching_field": "game", "type": "CATEGORY_TYPE_RESOURCE"}
      ]
    }
  }]
}`

const branchesMainOnly = `{
  "game_branches": [{
    "game": {"id": "gopR6Cufr3", "biz": "hk4e_global"},
    "main": {
      "package_id": "ScSYQBFhu9", "branch": "main", "password": "pw1",
      "tag": "6.6.0", "diff_tags": ["6.5.0"],
      "categories": [
        {"category_id": "10016", "matching_field": "game", "type": "CATEGORY_TYPE_RESOURCE"}
      ]
    }
  }]
}`

func TestParseBranchesMainAndPredl(t *testing.T) {
	bi, err := ParseBranches([]byte(branchesMainPredl), "gopR6Cufr3")
	if err != nil {
		t.Fatalf("ParseBranches: %v", err)
	}
	if bi.Main.PackageID != "ScSYQBFhu9" || bi.Main.Tag != "6.6.0" {
		t.Fatalf("main slot: %+v", bi.Main)
	}
	if len(bi.Main.DiffTags) != 2 || bi.Main.DiffTags[0] != "6.5.0" {
		t.Fatalf("main diff_tags: %+v", bi.Main.DiffTags)
	}
	if len(bi.Main.Categories) != 2 || bi.Main.Categories[1].MatchingField != "en-us" {
		t.Fatalf("main categories: %+v", bi.Main.Categories)
	}
	if bi.Main.Categories[1].Type != "CATEGORY_TYPE_AUDIO" {
		t.Fatalf("category type: %+v", bi.Main.Categories[1])
	}
	if bi.Main.IsEmpty() {
		t.Fatal("main unexpectedly empty")
	}
	if bi.PreDownload.IsEmpty() || bi.PreDownload.Tag != "6.7.0" {
		t.Fatalf("predl slot: %+v", bi.PreDownload)
	}
}

func TestParseBranchesMainOnly(t *testing.T) {
	bi, err := ParseBranches([]byte(branchesMainOnly), "gopR6Cufr3")
	if err != nil {
		t.Fatalf("ParseBranches: %v", err)
	}
	if !bi.PreDownload.IsEmpty() {
		t.Fatalf("predl should be empty: %+v", bi.PreDownload)
	}
}

func TestParseBranchesEmptyMainCategoriesIsError(t *testing.T) {
	const j = `{"game_branches":[{"game":{"id":"g"},"main":{"package_id":"p","tag":"6.6.0","categories":[]}}]}`
	if _, err := ParseBranches([]byte(j), "g"); err == nil {
		t.Fatal("expected error for empty Main categories")
	}
}

func TestParseBranchesEmptyPredlCategoriesIsOK(t *testing.T) {
	const j = `{"game_branches":[{"game":{"id":"g"},
	  "main":{"package_id":"p","tag":"6.6.0","categories":[{"category_id":"10016","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE"}]},
	  "pre_download":{"package_id":"q","tag":"6.7.0","categories":[]}}]}`
	bi, err := ParseBranches([]byte(j), "g")
	if err != nil {
		t.Fatalf("predl empty categories must not error: %v", err)
	}
	if bi.PreDownload.IsEmpty() || len(bi.PreDownload.Categories) != 0 {
		t.Fatalf("predl slot: %+v", bi.PreDownload)
	}
}

func TestParseBranchesGameNotPresent(t *testing.T) {
	if _, err := ParseBranches([]byte(branchesMainOnly), "OTHER"); err == nil {
		t.Fatal("expected error when requested game id absent")
	}
}
