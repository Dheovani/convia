/*
Package telemetry describes the running installation to whatever is watching it.

This is `M22-001`. Until now a Convia log line said what happened and nothing
about where: no service name, no version, no environment, and no way to tell two
instances apart. That is survivable while an installation is one process on one
machine and stops being survivable the moment it is not -- which is the point at
which somebody needs the answer and cannot go back and collect it.

The attributes are the OpenTelemetry resource ones by name, so that the logs and
the traces `M22-003` adds describe the same thing in the same words rather than
two vocabularies somebody has to join by hand.
*/
package telemetry

import (
	"log/slog"
	"os"
	"runtime/debug"
	"strings"
)

// Name is what this service calls itself, everywhere.
const Name = "convia"

/*
Service is the running installation, as anything watching it sees it.

Four attributes, and each one answers a question somebody asks during an
incident: what is this, which build, which deployment, and **which instance**.
The last is the one that is useless until a deployment is replicated and
impossible to add afterwards.
*/
type Service struct {
	// Name is always [Name]. It is a field so that the whole resource is one
	// value rather than a constant and three fields.
	Name string

	/*
		Version identifies the build. It is the revision the binary was built
		from, because Convia has no releases yet -- and a version that says
		`0.0.0` for every build is worse than one that says nothing, since it
		looks like an answer.
	*/
	Version string

	// Environment is development or production, as configuration decided.
	Environment string

	/*
		Instance tells two processes apart. It defaults to the hostname, which
		is right in a container and adequate anywhere else, and an operator who
		runs two on one host sets it.
	*/
	Instance string
}

/*
Describe returns the attributes every log line carries.

They are attached once, to the logger, rather than being passed at each call
site: an attribute somebody has to remember is one a new call site will
eventually forget, and these four are the ones that make a line findable at all.
*/
func (service Service) Describe() []slog.Attr {
	return []slog.Attr{
		slog.String("service.name", service.Name),
		slog.String("service.version", service.Version),
		slog.String("deployment.environment", service.Environment),
		slog.String("service.instance.id", service.Instance),
	}
}

/*
Describing builds the resource from the environment it is running in.

`instance` is what an operator configured, and empty means "work it out": the
hostname, or `unknown` when even that cannot be read. It never fails, because a
process that cannot describe itself should still start and still serve -- the
cost of getting this wrong is a harder incident, not a broken installation.
*/
func Describing(environment, instance string) Service {
	if instance = strings.TrimSpace(instance); instance == "" {
		instance = hostname()
	}

	return Service{
		Name:        Name,
		Version:     Version(),
		Environment: environment,
		Instance:    instance,
	}
}

/*
Version reports the revision this binary was built from.

It comes from the build information the toolchain embeds rather than from a
variable set by a linker flag, so that a binary built by anybody -- `go build`,
the Dockerfile, a contributor's laptop -- carries the same answer without anyone
remembering a flag. A working tree with uncommitted changes is marked, because
"which commit" is a lie when the answer is "that commit and some edits".

It is `unknown` when the binary was built without version control information,
which is what `go test` produces. That is honest and it is also the reason this
is not relied on for anything but describing a process.
*/
func Version() string {
	information, ok := debug.ReadBuildInfo()
	if !ok {
		return unknown
	}

	revision, modified := "", false
	for _, setting := range information.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value == "true"
		}
	}

	if revision == "" {
		return unknown
	}
	if len(revision) > shortRevision {
		revision = revision[:shortRevision]
	}
	if modified {
		return revision + "-modified"
	}
	return revision
}

const (
	unknown = "unknown"

	// shortRevision is how much of a commit hash is worth carrying on every
	// log line. Twelve characters name a commit unambiguously in any
	// repository anybody will run this from.
	shortRevision = 12
)

// hostname is the instance identifier nobody has to configure.
func hostname() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return unknown
	}
	return name
}
