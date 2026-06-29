package hub

import (
	"net"

	"github.com/Zouriel/zcoms/client"
	"github.com/Zouriel/zcoms/internal/comms/contacts"
)

// handleContactOp dispatches the contacts directory ops to the comms.db store.
// Both owner and agent may write contacts (addressing is not a crown jewel), so
// the store's guard always allows — but the caller is threaded through so the
// CRUD surface matches the agent-tier stores.
func (d *daemon) handleContactOp(conn net.Conn, req client.Request) {
	if d.contacts == nil {
		writeIPC(conn, client.Response{Error: "contacts store unavailable"})
		return
	}
	caller := contacts.CallerFrom(req.Caller)

	switch req.Op {
	case "contact_resolve":
		cs, err := d.contacts.Resolve(req.To)
		reply(conn, client.Response{OK: true, Contacts: cs}, err)

	case "contact_list":
		cs, err := d.contacts.List()
		reply(conn, client.Response{OK: true, Contacts: cs}, err)

	case "contact_create":
		if req.Contact == nil {
			writeIPC(conn, client.Response{Error: "contact_create needs a contact"})
			return
		}
		c, err := d.contacts.Create(caller, *req.Contact)
		reply(conn, client.Response{OK: true, Contacts: []client.Contact{c}}, err)

	case "contact_update":
		if req.Contact == nil {
			writeIPC(conn, client.Response{Error: "contact_update needs a contact"})
			return
		}
		err := d.contacts.Update(caller, *req.Contact)
		reply(conn, client.Response{OK: true}, err)

	case "contact_delete":
		if req.Contact == nil {
			writeIPC(conn, client.Response{Error: "contact_delete needs a contact id"})
			return
		}
		err := d.contacts.Delete(caller, req.Contact.ID)
		reply(conn, client.Response{OK: true}, err)

	case "contact_upsert":
		var cs []client.Contact
		if req.Contact != nil {
			cs = append(cs, *req.Contact)
		}
		err := d.contacts.Upsert(caller, cs)
		reply(conn, client.Response{OK: true}, err)
	}
}

// reply writes resp, or an error response when err != nil.
func reply(conn net.Conn, resp client.Response, err error) {
	if err != nil {
		writeIPC(conn, client.Response{Error: err.Error()})
		return
	}
	writeIPC(conn, resp)
}
