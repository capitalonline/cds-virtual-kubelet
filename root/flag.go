package root

import (
	"flag"
	"fmt"
	"k8s.io/klog"
	"os"
	"strings"

	"github.com/pkg/errors"
	"github.com/spf13/pflag"
)

type mapVar map[string]string

func (mv mapVar) String() string {
	var s string
	for k, v := range mv {
		if s == "" {
			s = fmt.Sprintf("%s=%v", k, v)
		} else {
			s += fmt.Sprintf(", %s=%v", k, v)
		}
	}
	return s
}

func (mv mapVar) Set(s string) error {
	split := strings.SplitN(s, "=", 2)
	if len(split) != 2 {
		return errors.Errorf("invalid format, must be `key=value`: %s", s)
	}

	_, ok := mv[split[0]]
	if ok {
		return errors.Errorf("duplicate key: %s", split[0])
	}
	mv[split[0]] = split[1]
	return nil
}

func (mv mapVar) Type() string {
	return "map"
}

func installFlags(flags *pflag.FlagSet, c *Opts) {
	flags.BoolVar(&c.EnableNodeLease, "enable-node-lease", c.EnableNodeLease, `use node leases (1.13) for node heartbeats`)
	flags.StringSliceVar(&c.TraceExporters, "trace-exporter", c.TraceExporters, fmt.Sprintf("sets the tracing exporter to use, available exporters: %s", AvailableTraceExporters()))
	flags.StringVar(&c.TraceConfig.ServiceName, "trace-service-name", c.TraceConfig.ServiceName, "sets the name of the service used to register with the trace exporter")
	flags.Var(mapVar(c.TraceConfig.Tags), "trace-tag", "add tags to include with traces in key=value form")
	flags.StringVar(&c.TraceSampleRate, "trace-sample-rate", c.TraceSampleRate, "set probability of tracing samples")

	flags.DurationVar(&c.StartupTimeout, "startup-timeout", c.StartupTimeout, "How long to wait for the virtual-kubelet to start")

	flagset := flag.NewFlagSet("klog", flag.PanicOnError)
	klog.InitFlags(flagset)
	flagset.VisitAll(func(f *flag.Flag) {
		f.Name = "klog." + f.Name
		flags.AddGoFlag(f)
	})
}

func getEnv(key, defaultValue string) string {
	value, found := os.LookupEnv(key)
	if found {
		return value
	}
	return defaultValue
}
