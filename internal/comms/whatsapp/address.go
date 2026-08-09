package whatsapp

import "strings"

// JID normalizes a phone number or JID into a WhatsApp JID.
//
// A value that already contains "@" is used as-is (a full @s.whatsapp.net or
// @lid jid); otherwise the digits are kept and "@s.whatsapp.net" appended, so a
// plain phone number like "+960 798-8692" becomes "9607988692@s.whatsapp.net".
func JID(to string) string {
	to = strings.TrimSpace(to)
	if strings.Contains(to, "@") {
		return to
	}

	var b strings.Builder
	for _, r := range to {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String() + "@s.whatsapp.net"
}

// LooksLikeNumber reports whether s is a bare phone number or JID (digits, +,
// spaces, dashes, or an @) rather than a contact name to look up.
func LooksLikeNumber(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	if strings.Contains(s, "@") {
		return true
	}

	for _, r := range s {
		if (r < '0' || r > '9') && r != '+' && r != ' ' && r != '-' && r != '(' && r != ')' {
			return false
		}
	}
	return true
}
