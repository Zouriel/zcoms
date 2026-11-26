package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

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
	o := projectInitOpts{transport: "telegram"}
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
	if o.transport != "telegram" && o.transport != "whatsapp" {
		return o, fmt.Errorf("transport must be telegram or whatsapp")
	}
	return o, nil
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
		fmt.Println(gerr)
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
			"  zc project say <text>   (post an update to the project's group)",
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
				text := strings.TrimSpace(strings.Join(args[1:], " "))
				if text == "" {
					return fmt.Errorf("say what? zc project say <text>")
				}
				return forwardToAgent("project say --path " + shellQuote(path) + " " + shellQuote(text))
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
