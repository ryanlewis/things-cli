package main

// ConfirmFlags is embedded in the commands that ask before a project-wide
// write. Kong resolves --yes from the config file's `assume_yes` key as well,
// so the prompt can be turned off once for machine use with the flag still
// deciding each run.
type ConfirmFlags struct {
	Yes bool `help:"Skip the confirmation prompt and proceed." short:"y" name:"yes"`
}
