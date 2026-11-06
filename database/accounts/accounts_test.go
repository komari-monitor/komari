package accounts

import (
	"testing"
	"time"

	"github.com/pquerna/otp/totp"
)

// TestHashPasswordIsSaltedAndStable pins the password hash contract: stable
// for the same input, different for different passwords, and never equal to
// the plaintext.
func TestHashPasswordIsSaltedAndStable(t *testing.T) {
	a := hashPasswd("correct horse battery staple")
	b := hashPasswd("correct horse battery staple")
	if a != b {
		t.Fatal("hash is not deterministic for the same password")
	}
	if hashPasswd("other password") == a {
		t.Fatal("different passwords produced the same hash")
	}
	if a == "correct horse battery staple" {
		t.Fatal("hash equals the plaintext")
	}
	if a == hashPasswd("correct horse battery staplex") {
		t.Fatal("hash is not sensitive to a one-character change")
	}
}

// TestCheckPassword covers the credential check used by the login endpoint.
func TestCheckPassword(t *testing.T) {
	resetAccountsState(t)
	user, err := CreateAccount("alice", "Strong-password1")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	if uuid, ok := CheckPassword("alice", "Strong-password1"); !ok || uuid != user.UUID {
		t.Fatalf("correct password rejected: uuid=%q ok=%v", uuid, ok)
	}
	if _, ok := CheckPassword("alice", "wrong-password"); ok {
		t.Fatal("wrong password accepted")
	}
	if _, ok := CheckPassword("nobody", "Strong-password1"); ok {
		t.Fatal("unknown user accepted")
	}
}

// TestForceResetPasswordChangesCredential guards the admin recovery path.
func TestForceResetPasswordChangesCredential(t *testing.T) {
	resetAccountsState(t)
	if _, err := CreateAccount("bob", "Old-password1"); err != nil {
		t.Fatalf("create account: %v", err)
	}
	if err := ForceResetPassword("bob", "New-password1"); err != nil {
		t.Fatalf("force reset: %v", err)
	}
	if _, ok := CheckPassword("bob", "Old-password1"); ok {
		t.Fatal("old password still works after reset")
	}
	if _, ok := CheckPassword("bob", "New-password1"); !ok {
		t.Fatal("new password does not work after reset")
	}
	if err := ForceResetPassword("nobody", "New-password1"); err == nil {
		t.Fatal("reset of a missing user should fail")
	}
}

// TestSessionLifecycle covers creation, lookup and deletion.
func TestSessionLifecycle(t *testing.T) {
	resetAccountsState(t)
	user, err := CreateAccount("carol", "Strong-password1")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	session, err := CreateSession(user.UUID, 3600, "ua", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if got, err := GetSession(session); err != nil || got != user.UUID {
		t.Fatalf("GetSession = %q, %v; want %q", got, err, user.UUID)
	}

	if err := DeleteSession(session); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := GetSession(session); err == nil {
		t.Fatal("deleted session still resolves")
	}
}

// TestExpiredSessionIsRejectedAndRemoved verifies GetSession both refuses an
// expired session and cleans it up.
func TestExpiredSessionIsRejectedAndRemoved(t *testing.T) {
	resetAccountsState(t)
	user, err := CreateAccount("dave", "Strong-password1")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	// expires=-1 puts the record in the past.
	session, err := CreateSession(user.UUID, -1, "ua", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	if _, err := GetSession(session); err == nil {
		t.Fatal("expired session resolved successfully")
	}
	if got := countSessions(t, "session = ?", session); got != 0 {
		t.Fatalf("expired session was not removed, %d rows remain", got)
	}
}

// TestRemoveExpiredSessionsOnlyDropsExpired pins the janitor's selectivity.
func TestRemoveExpiredSessionsOnlyDropsExpired(t *testing.T) {
	resetAccountsState(t)
	user, err := CreateAccount("erin", "Strong-password1")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	live, err := CreateSession(user.UUID, 3600, "ua", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create live session: %v", err)
	}
	stale, err := CreateSession(user.UUID, -1, "ua", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create stale session: %v", err)
	}

	if err := RemoveExpiredSessions(); err != nil {
		t.Fatalf("remove expired sessions: %v", err)
	}
	if _, err := GetSession(live); err != nil {
		t.Fatalf("live session was removed: %v", err)
	}
	if _, err := GetSession(stale); err == nil {
		t.Fatal("stale session survived the janitor")
	}
}

// TestUpdateUserRotatesPasswordAndDropsSessions pins the rule that changing a
// password invalidates every existing session.
func TestUpdateUserRotatesPasswordAndDropsSessions(t *testing.T) {
	resetAccountsState(t)
	user, err := CreateAccount("frank", "Strong-password1")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}
	session, err := CreateSession(user.UUID, 3600, "ua", "127.0.0.1", "password")
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	newPassword := "Rotated-password1"
	if err := UpdateUser(user.UUID, nil, &newPassword, nil); err != nil {
		t.Fatalf("update user: %v", err)
	}
	if _, err := GetSession(session); err == nil {
		t.Fatal("session survived a password rotation")
	}
	if _, ok := CheckPassword("frank", newPassword); !ok {
		t.Fatal("rotated password does not work")
	}
	if err := UpdateUser("missing-uuid", nil, &newPassword, nil); err == nil {
		t.Fatal("updating a missing user should fail")
	}
}

// TestTwoFactorLifecycle covers enable, verify and disable.
func TestTwoFactorLifecycle(t *testing.T) {
	resetAccountsState(t)
	user, err := CreateAccount("grace", "Strong-password1")
	if err != nil {
		t.Fatalf("create account: %v", err)
	}

	// Without a secret, verification must fail closed.
	if ok, err := Verify2Fa(user.UUID, "123456"); err != nil || ok {
		t.Fatalf("Verify2Fa before enabling = %v, %v; want false, nil", ok, err)
	}

	secret, img, err := Generate2Fa()
	if err != nil {
		t.Fatalf("generate 2fa: %v", err)
	}
	if secret == "" || img == nil {
		t.Fatal("Generate2Fa returned an empty secret or image")
	}
	if err := Enable2Fa(user.UUID, secret); err != nil {
		t.Fatalf("enable 2fa: %v", err)
	}

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate totp code: %v", err)
	}
	if ok, err := Verify2Fa(user.UUID, code); err != nil || !ok {
		t.Fatalf("Verify2Fa with a valid code = %v, %v; want true, nil", ok, err)
	}
	if ok, err := Verify2Fa(user.UUID, "000000"); err != nil || ok {
		t.Fatalf("Verify2Fa with a wrong code = %v, %v; want false, nil", ok, err)
	}

	if err := Disable2Fa(user.UUID); err != nil {
		t.Fatalf("disable 2fa: %v", err)
	}
	if ok, err := Verify2Fa(user.UUID, code); err != nil || ok {
		t.Fatalf("Verify2Fa after disabling = %v, %v; want false, nil", ok, err)
	}
}

// TestVerify2FaUnknownUserIsAnError distinguishes "no such user" from "wrong
// code" so callers do not silently treat a lookup failure as a pass.
func TestVerify2FaUnknownUserIsAnError(t *testing.T) {
	resetAccountsState(t)
	if _, err := Verify2Fa("missing-uuid", "123456"); err == nil {
		t.Fatal("Verify2Fa for a missing user should return an error")
	}
}
