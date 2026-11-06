package agentservice

import (
	"errors"
	"testing"
	"time"
)

func TestScopeValidationRejectsIncompleteOrUnknownSpace(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(Definition{ID: "drama.agent", Version: "1", Role: RoleBusiness, Application: ApplicationDrama, SpaceTypes: []string{SpaceDramaProject}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		def   string
		scope Scope
		code  ErrorCode
	}{
		{"unknown definition", "missing.agent", Scope{Application: ApplicationDrama, SpaceType: SpaceDramaProject, SpaceID: "p1"}, CodeInvalidRequest},
		{"application mismatch", "drama.agent", Scope{Application: ApplicationCanvas, SpaceType: SpaceDramaProject, SpaceID: "p1"}, CodeScopeMismatch},
		{"space type mismatch", "drama.agent", Scope{Application: ApplicationDrama, SpaceType: SpaceCanvas, SpaceID: "p1"}, CodeScopeMismatch},
		{"empty space", "drama.agent", Scope{Application: ApplicationDrama, SpaceType: SpaceDramaProject, SpaceID: " "}, CodeInvalidRequest},
	}
	for _, tc := range cases {
		_, err := registry.Resolve(tc.def, tc.scope)
		var agentErr *Error
		if !errors.As(err, &agentErr) || agentErr.Code != tc.code {
			t.Fatalf("%s: want %s, got %v", tc.name, tc.code, err)
		}
	}
	def, err := registry.Resolve("drama.agent", Scope{Application: ApplicationDrama, SpaceType: SpaceDramaProject, SpaceID: "p1"})
	if err != nil || def.Version != "1" {
		t.Fatalf("resolve: %+v %v", def, err)
	}
}

func TestRegistryRejectsDuplicateOrIncompleteDefinitions(t *testing.T) {
	registry := NewRegistry()
	good := Definition{ID: "x.agent", Version: "1", Role: RoleBusiness, Application: "x", SpaceTypes: []string{"x_space"}}
	if err := registry.Register(good); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(good); err == nil {
		t.Fatal("duplicate definition accepted")
	}
	for _, bad := range []Definition{{Version: "1", Role: RoleBusiness, Application: "x", SpaceTypes: []string{"s"}}, {ID: "y", Version: "1", Role: "admin", Application: "x", SpaceTypes: []string{"s"}}, {ID: "z", Version: "1", Role: RoleBusiness, Application: "x"}} {
		if err := registry.Register(bad); err == nil {
			t.Fatalf("incomplete definition accepted: %+v", bad)
		}
	}
	if got := registry.List("x"); len(got) != 1 || got[0].ID != "x.agent" {
		t.Fatalf("list: %+v", got)
	}
}

func TestWriterLeaseAllowsOneLiveWriter(t *testing.T) {
	now := time.Unix(1000, 0)
	free := Lease{}
	first, err := free.Acquire("w1", "run-1", now, time.Minute)
	if err != nil || first.Epoch != 1 || first.Owner != "w1" {
		t.Fatalf("first acquire: %+v %v", first, err)
	}
	if _, err := first.Acquire("w2", "run-2", now.Add(time.Second), time.Minute); !IsCode(err, CodeSessionConflict) {
		t.Fatalf("second writer while live: %v", err)
	}
	second, err := first.Acquire("w2", "run-2", now.Add(2*time.Minute), time.Minute)
	if err != nil || second.Epoch != 2 {
		t.Fatalf("acquire after expiry: %+v %v", second, err)
	}
	if err := second.Check("w1", 1, now.Add(2*time.Minute)); !IsCode(err, CodeStaleLease) {
		t.Fatalf("old epoch accepted: %v", err)
	}
	if err := second.Check("w2", 2, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("current writer rejected: %v", err)
	}
	if err := second.Check("w2", 2, now.Add(4*time.Minute)); !IsCode(err, CodeStaleLease) {
		t.Fatalf("expired lease accepted: %v", err)
	}
}

func TestCreateFingerprintTracksIdentityOnly(t *testing.T) {
	a := CreateSessionRequest{DefinitionID: "d", Scope: Scope{Application: "a", SpaceType: "s", SpaceID: "1"}, IdempotencyKey: "k"}
	b := a
	b.IdempotencyKey = "other"
	if a.Fingerprint() != b.Fingerprint() {
		t.Fatal("idempotency key must not change fingerprint")
	}
	c := a
	c.Scope.SpaceID = "2"
	if a.Fingerprint() == c.Fingerprint() {
		t.Fatal("scope change must change fingerprint")
	}
}
