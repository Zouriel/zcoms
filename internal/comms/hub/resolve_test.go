package hub

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/contacts"
)

// newDirectory returns a daemon backed by a throwaway contacts store holding cs.
func newDirectory(t *testing.T, cs ...client.Contact) *daemon {
	t.Helper()

	store, err := contacts.Open(filepath.Join(t.TempDir(), "comms.db"))
	if err != nil {
		t.Fatalf("open contacts: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	for _, c := range cs {
		if _, err := store.Create(contacts.CallerFrom("owner"), c); err != nil {
			t.Fatalf("create %q: %v", c.Name, err)
		}
	}
	return &daemon{contacts: store}
}

// A name from the contacts directory is the spelling callers actually have: it
// is what list_contacts hands an AI, and it is not a Telegram username.
func TestResolvesAContactNameToItsTelegramHandle(t *testing.T) {
	d := newDirectory(t, client.Contact{Name: "Shanna ❤️", Telegram: "@Zoella99", Phone: "+9607988692"})

	for _, name := range []string{"Shanna", "shanna", "Shanna ❤️"} {
		handle, contact, err := d.telegramHandleFor(name)
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if handle != "@Zoella99" {
			t.Errorf("%q resolved to %q, want @Zoella99", name, handle)
		}
		if contact != "Shanna ❤️" {
			t.Errorf("%q named %q, want the contact's own name", name, contact)
		}
	}
}

// Nobody by that name is not an error: it may still be a public username, which
// is what the caller falls back to.
func TestAnUnknownNameFallsThroughToTelegram(t *testing.T) {
	d := newDirectory(t, client.Contact{Name: "Shanna", Telegram: "@Zoella99"})

	handle, _, err := d.telegramHandleFor("durov")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if handle != "" {
		t.Errorf("resolved %q, want no handle so the caller tries it as a username", handle)
	}
}

// Guessing between two people sends a private message to the wrong one, and no
// retry undoes that.
func TestAnAmbiguousNameIsRefusedRatherThanGuessed(t *testing.T) {
	d := newDirectory(t,
		client.Contact{Name: "Sam Ali", Telegram: "@samali"},
		client.Contact{Name: "Sam Idris", Telegram: "@samidris"},
	)

	_, _, err := d.telegramHandleFor("Sam")
	if err == nil {
		t.Fatal("expected a refusal, got a silent pick")
	}
	for _, want := range []string{"Sam Ali", "Sam Idris"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q, so the caller cannot choose", err, want)
		}
	}
}

// Address falls back to a phone number, which Telegram cannot look up the way a
// username can. The reply should say that rather than pass a phone to Telegram
// and return whatever code comes back.
func TestAContactWithOnlyAPhoneSaysSo(t *testing.T) {
	d := newDirectory(t, client.Contact{Name: "Yaseen", Phone: "+9609354500"})

	_, _, err := d.telegramHandleFor("Yaseen")
	if err == nil {
		t.Fatal("expected an explanation, got none")
	}
	if !strings.Contains(err.Error(), "@username") {
		t.Errorf("error %q does not say what would fix it", err)
	}
}

// A numeric id is already a chat id, so it never touches the directory. This is
// the path every agent-tier caller takes, and it must stay free of TDLib.
func TestANumericIdResolvesWithoutLookingAnythingUp(t *testing.T) {
	d := &daemon{} // no contacts store, no TDLib session

	chatID, userID, err := d.resolveChat("  6244461583 ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if chatID != 6244461583 || userID != 6244461583 {
		t.Errorf("got chat %d user %d, want both 6244461583", chatID, userID)
	}
}

// A phone number is not a chat id, though ParseInt will happily read one as a
// number. Treating "+960..." as a chat id addresses a chat nobody meant.
func TestAPhoneNumberIsNotMistakenForAChatId(t *testing.T) {
	if _, ok := chatIDOf("+9609354500"); ok {
		t.Error("a phone number was read as a chat id")
	}
	if id, ok := chatIDOf("-1001234567890"); !ok || id != -1001234567890 {
		t.Error("a supergroup id must stay valid; its ids are negative")
	}
	if id, ok := chatIDOf("6244461583"); !ok || id != 6244461583 {
		t.Error("a plain chat id must stay valid")
	}
}
