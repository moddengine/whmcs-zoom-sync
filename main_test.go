package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadEnv(t *testing.T) {
	t.Setenv("EXISTING", "process")
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("# comment\nPLAIN=value\nQUOTED=\"two words\"\nexport EXISTING=file\n"), 0600); err != nil {
		t.Fatal(err)
	}

	if err := loadEnv(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("PLAIN") != "value" || os.Getenv("QUOTED") != "two words" || os.Getenv("EXISTING") != "process" {
		t.Fatal("environment was not loaded correctly")
	}
}

func TestFormatE164(t *testing.T) {
	if got := formatE164("0412 345 678"); got != "+61412345678" {
		t.Fatalf("got %q", got)
	}
	if got := formatE164("not a phone number"); got != "" {
		t.Fatalf("invalid number formatted as %q", got)
	}
}

func TestClientsToSyncKeepsExistingPhoneOwner(t *testing.T) {
	clients := []WHMCSClient{
		{ID: 1, PhoneNumber: "0412 345 678"},
		{ID: 2, PhoneNumber: "+61 412 345 678"},
		{ID: 3, PhoneNumber: "invalid"},
	}
	existing := map[string]ExternalContact{
		"2": {ID: "2", PhoneNumbers: []string{"+61412345678"}},
	}

	selected := clientsToSync(clients, existing)
	if len(selected) != 1 || selected[0].ID != 2 {
		t.Fatalf("selected clients: %#v", selected)
	}
}
