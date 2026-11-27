package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Zouriel/zcoms/client"
	"github.com/spf13/cobra"
)

// projectPath is the absolute working directory the agent uses to find the
// project binding from anywhere inside a bound folder.
func projectPath() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	return filepath.Abs(wd)
}

var instructionsCount = regexp.MustCompile(`instructions=(\d+)`)

// projectInitOpts are the answers `zc project init` collects.
type projectInitOpts struct {
	group, transport, follow string
	createAgentsMD           bool
}

// parseProjectInitFlags reads the optional flags of `project init`, which runs
// with flag parsing off so every other subcommand passes through untouched.
func parseProjectInitFlags(args []string) (projectInitOpts, error) {
	var o projectInitOpts
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			return args[i], nil
		}
		var err error
		switch a {
		case "--group":
			o.group, err = next()
		case "--follow":
			o.follow, err = next()
		case "--wa":
			o.transport = "whatsapp"
		case "--transport":
			o.transport, err = next()
		default:
			return o, fmt.Errorf("unknown flag %q for project init (flags: --group <name>, --follow on|off, --wa)", a)
		}
		if err != nil {
			return o, err
		}
	}
	if o.transport != "" && o.transport != "telegram" && o.transport != "whatsapp" {
		return o, fmt.Errorf("transport must be telegram or whatsapp")
	}
	return o, nil
}

// parsePlatform maps an answer to the platform prompt to a transport name,
// or "" when it is not one.
func parsePlatform(ans string) string {
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "t", "tg", "tele", "telegram":
		return "telegram"
	case "w", "wa", "whatsapp":
		return "whatsapp"
	}
	return ""
}

// isNoGroup reports whether a group lookup failed only because nothing has
// that name (the daemon's error crosses the socket as text).
func isNoGroup(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "no ") && strings.Contains(err.Error(), " group named ")
}

// splitMembers reads a comma or space separated member list.
func splitMembers(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' })
}

// offerCreateGroup asks to create the missing group and returns its name, or
// "" when the owner declines. The daemon always adds the owner; a Telegram
// group gets topics on so project updates have their own thread.
func offerCreateGroup(in *bufio.Reader, c groupCreator, transport, name string) (string, error) {
	yes, err := askYesNo(in, fmt.Sprintf("No group named %q. Create it?", name), true)
	if err != nil || !yes {
		return "", err
	}
	ans, err := promptLine(in, "Other members to add (usernames or phone numbers, comma separated; you are added anyway, Enter for none): ")
	if err != nil {
		return "", err
	}
	members := splitMembers(ans)
	var g *client.Group
	if transport == "telegram" {
		g, err = c.GroupCreateTopics(transport, name, members)
	} else {
		g, err = c.GroupCreate(transport, name, members)
	}
	if g != nil {
		fmt.Printf("Created %q\n", g.Name)
	}
	if err != nil {
		if g == nil {
			return "", fmt.Errorf("creating the group: %w", err)
		}
		fmt.Printf("warning: %v\n", err) // created, but a member could not be added
	}
	return g.Name, nil
}

// groupCreator is the part of the daemon client init needs to make a group.
type groupCreator interface {
	GroupCreate(transport, name string, members []string) (*client.Group, error)
	GroupCreateTopics(transport, name string, members []string) (*client.Group, error)
}

func askYesNo(r *bufio.Reader, prompt string, def bool) (bool, error) {
	hint := " [y/N] "
	if def {
		hint = " [Y/n] "
	}
	ans, err := promptLine(r, prompt+hint)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "":
		return def, nil
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	}
	return false, fmt.Errorf("answer y or n")
}

// runProjectInit asks the init questions and forwards one line to the agent.
func runProjectInit(args []string) error {
	o, err := parseProjectInitFlags(args)
	if err != nil {
		return err
	}
	path, err := projectPath()
	if err != nil {
		return err
	}
	in := bufio.NewReader(os.Stdin)

	// The platform comes first: the group name is resolved on it.
	for o.transport == "" {
		ans, err := promptLine(in, "Platform, Telegram or WhatsApp? [t/w] ")
		if err != nil {
			return err
		}
		o.transport = parsePlatform(ans)
		if o.transport == "" {
			fmt.Println("answer t (Telegram) or w (WhatsApp)")
		}
	}

	// The group name: checked against the daemon so a typo or an ambiguous name
	// is caught here, where the owner can answer again.
	for tries := 0; ; tries++ {
		if strings.TrimSpace(o.group) == "" {
			if o.group, err = promptLine(in, "Group name: "); err != nil {
				return err
			}
			o.group = strings.TrimSpace(o.group)
			if o.group == "" {
				continue
			}
		}
		c, ok := daemonClient()
		if !ok {
			break // the agent will resolve it and say so if it cannot
		}
		g, gerr := c.GroupGet(o.transport, o.group)
		if gerr == nil {
			o.group = g.Name
			break
		}
		if isNoGroup(gerr) {
			made, err := offerCreateGroup(in, c, o.transport, o.group)
			if err != nil {
				return err
			}
			if made != "" {
				o.group = made
				break
			}
		} else {
			fmt.Println(gerr)
		}
		if tries >= 2 {
			return fmt.Errorf("no group picked")
		}
		o.group = ""
	}

	switch strings.ToLower(o.follow) {
	case "on", "yes", "y":
		o.follow = "on"
	case "off", "no", "n":
		o.follow = "off"
	case "":
		yes, err := askYesNo(in, "Follow tasks (store messages, morning standup)?", false)
		if err != nil {
			return err
		}
		o.follow = "off"
		if yes {
			o.follow = "on"
		}
	default:
		return fmt.Errorf("--follow takes on or off")
	}

	// Only ask about AGENTS.md when the agent reports no instruction files.
	if res, perr := agentCommand("project probe --path " + shellQuote(path)); perr == nil {
		if m := instructionsCount.FindStringSubmatch(res); m != nil {
			if n, _ := strconv.Atoi(m[1]); n == 0 {
				if o.createAgentsMD, err = askYesNo(in, "No AGENTS.md or CLAUDE.md here. Create AGENTS.md with the project rule?", true); err != nil {
					return err
				}
			}
		}
	} else {
		return perr
	}

	line := fmt.Sprintf("project init --path %s --group %s --transport %s --follow %s",
		shellQuote(path), shellQuote(o.group), o.transport, o.follow)
	if o.createAgentsMD {
		line += " --create-agents-md"
	}
	return forwardToAgent(line)
}

// projectSayLine parses `zc project say [--thread <t>] [--file <path>]...
// <text>` and builds the line forwarded to the agent. Files become absolute
// paths and must exist; text may be empty only when files are sent.
func projectSayLine(path string, args []string) (string, error) {
	var thread string
	var files, words []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--thread" || a == "--file":
			if i+1 >= len(args) {
				return "", fmt.Errorf("%s needs a value", a)
			}
			i++
			if a == "--thread" {
				thread = strings.TrimSpace(args[i])
				continue
			}
			files = append(files, args[i])
		case strings.HasPrefix(a, "--thread="):
			thread = strings.TrimSpace(strings.TrimPrefix(a, "--thread="))
		case strings.HasPrefix(a, "--file="):
			files = append(files, strings.TrimPrefix(a, "--file="))
		default:
			words = append(words, a)
		}
	}
	text := strings.TrimSpace(strings.Join(words, " "))
	if text == "" && len(files) == 0 {
		return "", fmt.Errorf("say what? zc project say [--thread <name>] [--file <path>]... <text>")
	}
	line := "project say --path " + shellQuote(path)
	if thread != "" {
		line += " --thread " + shellQuote(thread)
	}
	for _, f := range files {
		abs, err := filepath.Abs(expandUserPath(f))
		if err != nil {
			return "", err
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("file not found: %s", f)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("%s is not a regular file", f)
		}
		line += " --file " + shellQuote(abs)
	}
	if text != "" {
		line += " " + shellQuote(text)
	}
	return line, nil
}

func init() {
	projectCommand := &cobra.Command{
		Use:   "project <init|status|remove|members|alias|tasks|follow|standup|sync|say> [args…]",
		Short: "Bind this repo to a group: the agent answers calls there from the code",
		Long: "Project groups, run from inside the project folder (the agent finds the\n" +
			"binding from anywhere inside it):\n" +
			"  zc project init [--group <name>] [--follow on|off] [--wa]\n" +
			"  zc project status | remove [--purge] | sync\n" +
			"  zc project members | alias add|rm <member> <alias>\n" +
			"  zc project tasks [done|drop <id> | assign <id> <member>]\n" +
			"  zc project follow on|off | standup now\n" +
			"  zc project say [--thread <name|id>] [--file <path>]... <text>\n" +
			"      post an update (and files, photos as photos, 20 MB each) to the\n" +
			"      project's group; --thread posts in that thread, creating it if needed",
		DisableFlagParsing: true,
		SilenceUsage:       true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
				return cmd.Help()
			}
			if args[0] == "init" {
				return runProjectInit(args[1:])
			}
			path, err := projectPath()
			if err != nil {
				return err
			}
			if args[0] == "say" {
				line, err := projectSayLine(path, args[1:])
				if err != nil {
					return err
				}
				return forwardToAgent(line)
			}
			line := "project " + args[0] + " --path " + shellQuote(path)
			if rest := quoteArgs(args[1:]); rest != "" {
				line += " " + rest
			}
			return forwardToAgent(line)
		},
	}
	rootCmd.AddCommand(projectCommand)
}
