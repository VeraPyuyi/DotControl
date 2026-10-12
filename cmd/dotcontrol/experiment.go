package main

import (
	"context"
	"errors"
	"github.com/dotcontrol/dotcontrol/internal/experiment"
)

// This helper needs only saved references; the desktop agent need not be running.
func experimentCLI(ctx context.Context, dir string, args []string) error {
	if len(args) == 0 || (args[0] != "submit" && args[0] != "status" && args[0] != "stop") {
		return errors.New("experiment requires submit/status/stop")
	}
	f := flags("experiment " + args[0])
	profile := f.String("profile", "", "Saved server profile ID")
	id := f.String("experiment-id", "", "Stable experiment ID; same ID never relaunches")
	if e := f.Parse(args[1:]); e != nil {
		return e
	}
	if *profile == "" || !experiment.ValidID(*id) || len(f.Args()) != 0 {
		return errors.New("invalid_experiment_request")
	}
	s, e := experiment.New(dir, experiment.Options{}).Run(ctx, args[0], *profile, *id)
	if e != nil {
		return e
	}
	output(s)
	return nil
}
