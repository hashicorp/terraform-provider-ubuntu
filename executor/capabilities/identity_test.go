// Copyright IBM Corp. 2026

package capabilities

import (
	"reflect"
	"testing"
)

func TestResolveIdentityUserIncludesPrimaryAndSupplementaryGroups(t *testing.T) {
	t.Parallel()

	identity := resolveIdentityUser("alice", []passwdEntry{{
		Name:    "alice",
		UID:     1000,
		GID:     1000,
		Comment: "Alice Example",
		Home:    "/home/alice",
		Shell:   "/bin/bash",
	}}, []groupEntry{
		{Name: "developers", GID: 1000},
		{Name: "docker", GID: 1100, Members: []string{"alice"}},
		{Name: "wheel", GID: 1200, Members: []string{"alice", "bob"}},
	})

	if identity == nil {
		t.Fatal("expected identity result")
	}
	if identity.PrimaryGroup != "developers" {
		t.Fatalf("unexpected primary group: %#v", identity)
	}
	if !reflect.DeepEqual(identity.Groups, []string{"docker", "wheel"}) {
		t.Fatalf("unexpected supplementary groups: %#v", identity.Groups)
	}
	if identity.Shell != "/bin/bash" || identity.Comment != "Alice Example" {
		t.Fatalf("unexpected identity payload: %#v", identity)
	}
}

func TestParsePasswdAndGroupEntries(t *testing.T) {
	t.Parallel()

	passwdEntries, err := parsePasswdEntries("root:x:0:0:root:/root:/bin/bash\nalice:x:1000:1000:Alice Example:/home/alice:/bin/bash\n")
	if err != nil {
		t.Fatalf("parsePasswdEntries returned error: %v", err)
	}
	if len(passwdEntries) != 2 || passwdEntries[1].Name != "alice" || passwdEntries[1].UID != 1000 {
		t.Fatalf("unexpected passwd entries: %#v", passwdEntries)
	}

	groupEntries, err := parseGroupEntries("root:x:0:\ndevelopers:x:1000:alice,bob\n")
	if err != nil {
		t.Fatalf("parseGroupEntries returned error: %v", err)
	}
	if len(groupEntries) != 2 || groupEntries[1].Name != "developers" || groupEntries[1].GID != 1000 {
		t.Fatalf("unexpected group entries: %#v", groupEntries)
	}
	if !reflect.DeepEqual(groupEntries[1].Members, []string{"alice", "bob"}) {
		t.Fatalf("unexpected group members: %#v", groupEntries[1].Members)
	}
}
