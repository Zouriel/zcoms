package cmd

import (
	"bufio"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/Zouriel/zcoms/client"
)

type fakeCreator struct {
	topics  bool
	members []string
}

func (f *fakeCreator) GroupCreate(_, name string, m []string) (*client.Group, error) {
	f.members = m
	return &client.Group{Name: name}, nil
}

func (f *fakeCreator) GroupCreateTopics(_, name string, m []string) (*client.Group, error) {
	f.topics, f.members = true, m
	return &client.Group{Name: name}, nil
}

func TestIsNoGroup(t *testing.T) {
	if !isNoGroup(errors.New(`no telegram group named "X"`)) {
		t.Fatal("not-found not detected")
	}
	if isNoGroup(errors.New(`"X" matches several groups (a, b): say which one`)) {
		t.Fatal("ambiguous treated as not found")
	}
}

func TestOfferCreateGroup(t *testing.T) {
	cases := []struct {
		name, transport, input string
		want                   string
		topics                 bool
		members                []string
	}{
		{"telegram with members", "telegram", "y\n@ali, +9607771234\n", "Proj", true, []string{"@ali", "+9607771234"}},
		{"default yes, no members", "telegram", "\n\n", "Proj", true, nil},
		{"whatsapp no topics", "whatsapp", "y\n\n", "Proj", false, nil},
		{"declined", "telegram", "n\n", "", false, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeCreator{}
			got, err := offerCreateGroup(bufio.NewReader(strings.NewReader(tc.input)), f, tc.transport, "Proj")
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want || f.topics != tc.topics || !slices.Equal(f.members, tc.members) {
				t.Fatalf("got %q topics=%v members=%q", got, f.topics, f.members)
			}
		})
	}
}
