package kurogames

import "testing"

const krsdkCacheFixture = `{"account_list":[` +
	`{"cuid":537195734,"email":"a@example.com","username":"U547195734A","token":"TOKEN_A_aaaaaaaa","loginType":13,"thirdNickName":""},` +
	`{"cuid":535788351,"email":"b@example.com","username":"U545788351A","token":"TOKEN_B_bbbbbbbb","loginType":13,"thirdNickName":""}` +
	`],"last_login_cuid":"535788351"}`

func TestParseKRSDKAccounts(t *testing.T) {
	got, err := parseKRSDKAccounts([]byte(krsdkCacheFixture))
	if err != nil {
		t.Fatalf("parseKRSDKAccounts: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d accounts, want 2", len(got))
	}
	if got[0].ID != "537195734" || got[0].Email != "a@example.com" || got[0].Username != "U547195734A" {
		t.Errorf("account[0] = %+v", got[0])
	}
	if got[0].Active {
		t.Errorf("account[0] should not be active")
	}
	if !got[1].Active || got[1].ID != "535788351" {
		t.Errorf("account[1] should be the active one, got %+v", got[1])
	}
	if got[0].UID != "" || got[1].UID != "" {
		t.Errorf("UID must be empty at parse time")
	}
}

func TestParseKRSDKAccounts_Malformed(t *testing.T) {
	if _, err := parseKRSDKAccounts([]byte("not json")); err == nil {
		t.Fatal("expected error on malformed json")
	}
}
