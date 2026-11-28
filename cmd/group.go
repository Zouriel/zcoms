package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Zouriel/zcoms/client"

	"github.com/spf13/cobra"
)

// groupCmd builds `zc tg group` / `zc wa group`: create, add, list, members
// and view, all served by the comms daemon on the given transport.
func groupCmd(transportName, label string) *cobra.Command {
	root := &cobra.Command{
		Use:   "group",
		Short: label + " groups: create, add members, list, members, view, threads",
		Long: "Group commands on " + label + ", served by the comms daemon. A group is named by\n" +
			"its title (as `group list` shows it), an alias, or its native id. Creating a\n" +
			"group always adds the owner too (set with `zc config owner <contact>`).\n" +
			"The agent ignores every group until you turn it on with `zc group on <group>`.",
	}

	daemon := func() (*client.Client, error) {
		c, ok := daemonClient()
		if !ok {
			return nil, fmt.Errorf("the comms daemon isn't running")
		}
		return c, nil
	}

	var (
		topics    bool
		disappear string
	)
	create := &cobra.Command{
		Use:   "create <name> <member>...",
		Short: "Create a group with these members (the owner is always added)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemon()
			if err != nil {
				return err
			}
			o := client.GroupCreateOptions{Topics: topics}
			if disappear != "" {
				if o.Disappear, err = parseDisappear(disappear); err != nil {
					return err
				}
			}
			g, err := c.GroupCreateWith(transportName, args[0], args[1:], o)
			if g != nil {
				fmt.Printf("Created %q (%s)\n", g.Name, g.NativeID)
				if o.Disappear > 0 && err == nil {
					fmt.Printf("Messages disappear after %s (every thread). Use `zc tg send --pin` for a pinned message that stays.\n", formatDisappear(o.Disappear))
				}
			}
			return err
		},
	}

	add := &cobra.Command{
		Use:   "add <group> <member>...",
		Short: "Add members to a group",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemon()
			if err != nil {
				return err
			}
			if err := c.GroupAdd(transportName, args[0], args[1:]); err != nil {
				return err
			}
			fmt.Printf("Added %s to %s\n", strings.Join(args[1:], ", "), args[0])
			return nil
		},
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List the groups this account is in (and sync them into the registry)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemon()
			if err != nil {
				return err
			}
			groups, err := c.GroupList(transportName)
			if err != nil {
				return err
			}
			if len(groups) == 0 {
				fmt.Println("No groups.")
				return nil
			}
			for _, g := range groups {
				var flags []string
				if g.Listen {
					flags = append(flags, "on")
				}
				if g.Follow {
					flags = append(flags, "follow")
				}
				line := fmt.Sprintf("%-32s %s", g.Name, g.NativeID)
				if len(flags) > 0 {
					line += "  [" + strings.Join(flags, ", ") + "]"
				}
				fmt.Println(line)
			}
			return nil
		},
	}

	members := &cobra.Command{
		Use:   "members <group>",
		Short: "List a group's members",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemon()
			if err != nil {
				return err
			}
			ms, err := c.GroupMembers(transportName, args[0])
			if err != nil {
				return err
			}
			for _, m := range ms {
				line := m.Name
				if m.Username != "" {
					line += " " + m.Username
				}
				line += "  (" + m.ID + ")"
				if m.IsSelf {
					line += "  [this account]"
				}
				fmt.Println(line)
			}
			return nil
		},
	}

	var limit int
	var around string
	var follow bool
	view := &cobra.Command{
		Use:   "view <group>",
		Short: "Show a group's latest messages (--around a message, --follow to stay attached)",
		Long: "Shows the latest messages (50 by default). --around <msg id> shows --limit\n" +
			"messages before and after that message. --follow stays attached like tail:\n" +
			"new messages print as they arrive and each line you type is sent to the group.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemon()
			if err != nil {
				return err
			}
			msgs, err := c.GroupRead(transportName, args[0], client.GroupReadOpts{Limit: limit, Around: around})
			if err != nil {
				return err
			}
			seen := map[string]bool{}
			for _, m := range msgs {
				printGroupMessage(m)
				seen[m.ID] = true
			}
			if !follow {
				return nil
			}
			return followGroup(c, transportName, args[0], seen)
		},
	}
	view.Flags().IntVar(&limit, "limit", 50, "How many messages (each side, with --around)")
	view.Flags().StringVar(&around, "around", "", "Center the window on this message id")
	view.Flags().BoolVar(&follow, "follow", false, "Stay attached: print new messages and send what you type")

	if transportName == "telegram" {
		create.Flags().BoolVar(&topics, "topics", false, "Create it with threads (forum topics) on")
	}

	topic := &cobra.Command{
		Use:   "topic <group> <name>",
		Short: "Start a thread (forum topic) in a group",
		Long: "Creates a thread in the group and prints its id. When the group has topics\n" +
			"off and this account created it, topics are turned on first; otherwise an\n" +
			"admin has to enable them in the group settings. WhatsApp groups have no threads.",
		Args: cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemon()
			if err != nil {
				return err
			}
			t, err := c.TopicCreate(transportName, args[0], strings.Join(args[1:], " "))
			if err != nil {
				return err
			}
			fmt.Printf("Started thread %q (id %s); send to it with --thread %s\n", t.Name, t.ID, t.ID)
			return nil
		},
	}

	topicsCmd := &cobra.Command{
		Use:   "topics <group>",
		Short: "List a group's threads (forum topics)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemon()
			if err != nil {
				return err
			}
			ts, err := c.TopicList(transportName, args[0])
			if err != nil {
				return err
			}
			if len(ts) == 0 {
				fmt.Println("No threads.")
				return nil
			}
			for _, t := range ts {
				fmt.Printf("%-8s %s\n", t.ID, t.Name)
			}
			return nil
		},
	}

	renameCmd := &cobra.Command{
		Use:   "topic-rename <group> <thread id or name> <new name>",
		Short: "Rename a group's thread (forum topic)",
		Args:  cobra.MinimumNArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemon()
			if err != nil {
				return err
			}
			t, err := c.TopicRename(transportName, args[0], args[1], strings.Join(args[2:], " "))
			if err != nil {
				return err
			}
			fmt.Printf("Renamed thread %s to %q\n", t.ID, t.Name)
			return nil
		},
	}

	root.AddCommand(create, add, list, members, view, topic, topicsCmd, renameCmd)
	if transportName == "telegram" {
		create.Flags().StringVar(&disappear, "disappear", "", "Turn on disappearing messages for the whole group: 1d, 7d, 2w, ... (threads share it)")
		root.AddCommand(disappearCmd(daemon))
	}
	return root
}

func printGroupMessage(m client.GroupMessage) {
	who := m.SenderName
	if m.Outgoing {
		who = "you"
	}
	body := m.Text
	if body == "" {
		body = "[" + strings.TrimPrefix(m.Kind, "message") + "]"
	}
	reply := ""
	if m.ReplyToID != "" {
		reply = " (re " + m.ReplyToID + ")"
	}
	fmt.Printf("[%s] #%s %s%s: %s\n", time.Unix(m.Date, 0).Format("Jan 02 15:04"), m.ID, who, reply, body)
}

// followGroup polls the group for new messages and sends each typed line,
// until stdin closes.
func followGroup(c *client.Client, transportName, group string, seen map[string]bool) error {
	g, err := c.GroupGet(transportName, group)
	if err != nil {
		return err
	}
	fmt.Printf("Following %q. Type to send; Ctrl-D to stop.\n", g.Name)

	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			if _, err := c.SendOn(transportName, g.NativeID, line); err != nil {
				fmt.Fprintf(os.Stderr, "send failed: %v\n", err)
			}
		}
	}()

	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-done:
			return nil
		case <-tick.C:
			msgs, err := c.GroupRead(transportName, g.NativeID, client.GroupReadOpts{Limit: 20})
			if err != nil {
				fmt.Fprintf(os.Stderr, "read failed: %v\n", err)
				continue
			}
			for _, m := range msgs {
				if seen[m.ID] {
					continue
				}
				seen[m.ID] = true
				printGroupMessage(m)
			}
		}
	}
}

func init() {
	tgCmd.AddCommand(groupCmd("telegram", "Telegram"))
}

// disappearCmd builds `zc tg group disappear`: show or set a group's
// disappearing-messages timer, or one thread's with --thread. Telegram keeps
// one timer per chat; a thread timer is enforced by the daemon, which deletes
// the thread's expired messages itself (never pinned ones).
func disappearCmd(daemon func() (*client.Client, error)) *cobra.Command {
	var thread string
	cmd := &cobra.Command{
		Use:   "disappear <group> [1d|7d|2w|...|off] [--thread <name|id>]",
		Short: "Show or set disappearing messages for a group or one thread",
		Long: "Without --thread, set Telegram's own timer for the whole group (every\n" +
			"thread shares it). With --thread, only that thread's messages disappear:\n" +
			"Telegram has no per-thread timer, so the comms daemon deletes them once\n" +
			"they are older than the timer (checked every minute; it needs the delete\n" +
			"right for other people's messages). Either way only messages sent from then\n" +
			"on are affected, and pinned messages stay (`zc tg send --pin`).",
		Args:         cobra.RangeArgs(1, 2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := daemon()
			if err != nil {
				return err
			}
			group := args[0]
			if len(args) == 1 {
				whole, timers, err := c.ThreadTimers(group)
				if err != nil {
					return err
				}
				fmt.Printf("Disappearing messages in %s: %s\n", group, formatDisappear(whole))
				for _, t := range timers {
					name := t.Name
					if name == "" {
						name = "thread " + t.ThreadID
					}
					fmt.Printf("  thread %q: %s\n", name, formatDisappear(time.Duration(t.Seconds)*time.Second))
				}
				return nil
			}
			d, err := parseDisappear(args[1])
			if err != nil {
				return err
			}
			if thread != "" {
				if _, err := c.SetThreadDisappear(group, thread, d); err != nil {
					return err
				}
				if d == 0 {
					fmt.Printf("Disappearing messages off in thread %q of %s\n", thread, group)
					return nil
				}
				fmt.Printf("Messages in thread %q of %s now disappear after %s (only that thread; pinned ones stay)\n", thread, group, formatDisappear(d))
				return nil
			}
			if _, err := c.SetDisappear(group, d); err != nil {
				return err
			}
			if d == 0 {
				fmt.Printf("Disappearing messages off in %s\n", group)
				return nil
			}
			fmt.Printf("Messages in %s now disappear after %s (every thread)\n", group, formatDisappear(d))
			return nil
		},
	}
	cmd.Flags().StringVar(&thread, "thread", "", "Only this thread (topic name or id)")
	return cmd
}
