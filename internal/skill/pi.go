package skill

func init() { register(piAgent{}) }

type piAgent struct{}

func (piAgent) Name() string { return "pi" }

// DefaultDir resolves the skill directory inside Pi's agent config
// directory. $PI_CODING_AGENT_DIR replaces the whole ~/.pi/agent default, so
// honour it when set and fall back to ~/.pi/agent. Pi expands a leading tilde
// in that variable before reading from it, so we have to expand it too.
func (piAgent) DefaultDir() (string, error) {
	return resolveAgentDir("PI_CODING_AGENT_DIR", expandsTilde, ".pi", "agent")
}

func (piAgent) Files() map[string][]byte { return sharedFiles }
