package capabilities

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type passwdEntry struct {
	Name    string
	UID     int
	GID     int
	Comment string
	Home    string
	Shell   string
}

type groupEntry struct {
	Name    string
	GID     int
	Members []string
}

func LookupUser(name string) (*IdentityUser, error) {
	passwdEntries, err := readPasswdEntries("/etc/passwd")
	if err != nil {
		return nil, err
	}
	groupEntries, err := readGroupEntries("/etc/group")
	if err != nil {
		return nil, err
	}
	return resolveIdentityUser(name, passwdEntries, groupEntries), nil
}

func LookupGroup(name string) (*IdentityGroup, error) {
	groupEntries, err := readGroupEntries("/etc/group")
	if err != nil {
		return nil, err
	}
	return resolveIdentityGroup(name, groupEntries), nil
}

func lookupUserID(name string) (int, error) {
	identity, err := LookupUser(name)
	if err != nil {
		return 0, err
	}
	if identity == nil {
		return 0, fmt.Errorf("user %q not found", name)
	}
	return identity.UID, nil
}

func lookupGroupID(name string) (int, error) {
	identity, err := LookupGroup(name)
	if err != nil {
		return 0, err
	}
	if identity == nil {
		return 0, fmt.Errorf("group %q not found", name)
	}
	return identity.GID, nil
}

func resolveIdentityUser(name string, passwdEntries []passwdEntry, groupEntries []groupEntry) *IdentityUser {
	var userEntry *passwdEntry
	for i := range passwdEntries {
		if passwdEntries[i].Name == name {
			userEntry = &passwdEntries[i]
			break
		}
	}
	if userEntry == nil {
		return nil
	}

	identity := &IdentityUser{
		Name:    userEntry.Name,
		UID:     userEntry.UID,
		GID:     userEntry.GID,
		Comment: userEntry.Comment,
		Home:    userEntry.Home,
		Shell:   userEntry.Shell,
	}

	seenGroups := make(map[string]struct{})
	for _, entry := range groupEntries {
		if entry.GID == userEntry.GID && identity.PrimaryGroup == "" {
			identity.PrimaryGroup = entry.Name
		}
		for _, member := range entry.Members {
			if member != userEntry.Name {
				continue
			}
			if entry.GID == userEntry.GID || entry.Name == identity.PrimaryGroup {
				break
			}
			if _, exists := seenGroups[entry.Name]; !exists {
				identity.Groups = append(identity.Groups, entry.Name)
				seenGroups[entry.Name] = struct{}{}
			}
			break
		}
	}

	return identity
}

func resolveIdentityGroup(name string, groupEntries []groupEntry) *IdentityGroup {
	for _, entry := range groupEntries {
		if entry.Name == name {
			return &IdentityGroup{Name: entry.Name, GID: entry.GID}
		}
	}
	return nil
}

func readPasswdEntries(path string) ([]passwdEntry, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parsePasswdEntries(string(content))
}

func readGroupEntries(path string) ([]groupEntry, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseGroupEntries(string(content))
}

func parsePasswdEntries(content string) ([]passwdEntry, error) {
	entries := make([]passwdEntry, 0)
	for lineNumber, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.SplitN(line, ":", 7)
		if len(fields) != 7 {
			return nil, fmt.Errorf("parse passwd line %d: expected 7 fields, got %d", lineNumber+1, len(fields))
		}
		uid, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("parse passwd line %d uid: %w", lineNumber+1, err)
		}
		gid, err := strconv.Atoi(fields[3])
		if err != nil {
			return nil, fmt.Errorf("parse passwd line %d gid: %w", lineNumber+1, err)
		}
		entries = append(entries, passwdEntry{
			Name:    fields[0],
			UID:     uid,
			GID:     gid,
			Comment: fields[4],
			Home:    fields[5],
			Shell:   fields[6],
		})
	}
	return entries, nil
}

func parseGroupEntries(content string) ([]groupEntry, error) {
	entries := make([]groupEntry, 0)
	for lineNumber, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.SplitN(line, ":", 4)
		if len(fields) != 4 {
			return nil, fmt.Errorf("parse group line %d: expected 4 fields, got %d", lineNumber+1, len(fields))
		}
		gid, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, fmt.Errorf("parse group line %d gid: %w", lineNumber+1, err)
		}
		members := make([]string, 0)
		if fields[3] != "" {
			for _, member := range strings.Split(fields[3], ",") {
				member = strings.TrimSpace(member)
				if member != "" {
					members = append(members, member)
				}
			}
		}
		entries = append(entries, groupEntry{Name: fields[0], GID: gid, Members: members})
	}
	return entries, nil
}
