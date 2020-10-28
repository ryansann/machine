package provision

import (
	"bytes"
	"fmt"
	"text/template"

	"github.com/rancher/machine/libmachine/auth"
	"github.com/rancher/machine/libmachine/drivers"
	"github.com/rancher/machine/libmachine/engine"
	"github.com/rancher/machine/libmachine/log"
	"github.com/rancher/machine/libmachine/provision/pkgaction"
	"github.com/rancher/machine/libmachine/registry"
	"github.com/rancher/machine/libmachine/swarm"
)

func init() {
	Register("Fedora-CoreOS", &RegisteredProvisioner{
		New: NewFedoraCoreOSProvisioner,
	})
}

// NewFedoraCoreOSProvisioner creates a new provisioner for a driver
func NewFedoraCoreOSProvisioner(d drivers.Driver) Provisioner {
	return &FedoraCoreOSProvisioner{
		NewSystemdProvisioner("fedora", d),
	}
}

// FedoraCoreOSProvisioner is a provisioner based on the CoreOS provisioner
type FedoraCoreOSProvisioner struct {
	SystemdProvisioner
}

// String returns the name of the provisioner
func (provisioner *FedoraCoreOSProvisioner) String() string {
	return "Fedora CoreOS"
}

// SetHostname sets the hostname of the remote machine
func (provisioner *FedoraCoreOSProvisioner) SetHostname(hostname string) error {
	log.Debugf("SetHostname: %s", hostname)

	command := fmt.Sprintf("sudo hostnamectl set-hostname %s", hostname)
	if _, err := provisioner.SSHCommand(command); err != nil {
		return err
	}

	return nil
}

// GenerateDockerOptions formats a systemd drop-in unit which adds support for
// Docker Machine
func (provisioner *FedoraCoreOSProvisioner) GenerateDockerOptions(dockerPort int) (*DockerOptions, error) {
	var (
		engineCfg bytes.Buffer
	)

	driverNameLabel := fmt.Sprintf("provider=%s", provisioner.Driver.DriverName())
	provisioner.EngineOptions.Labels = append(provisioner.EngineOptions.Labels, driverNameLabel)

	engineConfigTmpl := `[Service]
ExecStart=
ExecStart=/usr/bin/dockerd \\
          --host=fd:// \\
          --exec-opt native.cgroupdriver=systemd \\
          --host=tcp://0.0.0.0:{{.DockerPort}} \\
          --tlsverify \\
          --tlscacert {{.AuthOptions.CaCertRemotePath}} \\
          --tlscert {{.AuthOptions.ServerCertRemotePath}} \\
          --tlskey {{.AuthOptions.ServerKeyRemotePath}}{{ range .EngineOptions.Labels }} \\
          --label {{.}}{{ end }}{{ range .EngineOptions.InsecureRegistry }} \\
          --insecure-registry {{.}}{{ end }}{{ range .EngineOptions.RegistryMirror }} \\
          --registry-mirror {{.}}{{ end }}{{ range .EngineOptions.ArbitraryFlags }} \\
          -{{.}}{{ end }} \\
          \$OPTIONS
Environment={{range .EngineOptions.Env}}{{ printf "%q" . }} {{end}}
`

	t, err := template.New("engineConfig").Parse(engineConfigTmpl)
	if err != nil {
		return nil, err
	}

	engineConfigContext := EngineConfigContext{
		DockerPort:    dockerPort,
		AuthOptions:   provisioner.AuthOptions,
		EngineOptions: provisioner.EngineOptions,
	}

	t.Execute(&engineCfg, engineConfigContext)

	return &DockerOptions{
		EngineOptions:     engineCfg.String(),
		EngineOptionsPath: provisioner.DaemonOptionsFile,
	}, nil
}

// CompatibleWithHost returns whether or not this provisoner is compatible
// with the target host
func (provisioner *FedoraCoreOSProvisioner) CompatibleWithHost() bool {
	isFedora := provisioner.OsReleaseInfo.ID == "fedora"
	isCoreOS := provisioner.OsReleaseInfo.VariantID == "coreos"
	return isFedora && isCoreOS
}

// Package installs a package on the remote host. The Fedora CoreOS provisioner
// does not support (or need) any package installation
func (provisioner *FedoraCoreOSProvisioner) Package(name string, action pkgaction.PackageAction) error {
	return nil
}

// Provision provisions the machine
func (provisioner *FedoraCoreOSProvisioner) Provision(swarmOptions swarm.Options, authOptions auth.Options, engineOptions engine.Options, registryOptions registry.Options) error {
	provisioner.SwarmOptions = swarmOptions
	provisioner.AuthOptions = authOptions
	provisioner.EngineOptions = engineOptions
	provisioner.RegistryOptions = registryOptions

	if err := provisioner.SetHostname(provisioner.Driver.GetMachineName()); err != nil {
		return err
	}

	if err := makeDockerOptionsDir(provisioner); err != nil {
		return err
	}

	log.Debugf("Preparing certificates")
	provisioner.AuthOptions = setRemoteAuthOptions(provisioner)

	log.Debugf("Setting up certificates")
	if err := ConfigureAuth(provisioner); err != nil {
		return err
	}

	log.Debug("Logging into private registry")
	if err := dockerLoginGeneric(provisioner, registryOptions); err != nil {
		return err
	}

	log.Debug("Configuring swarm")
	err := configureSwarm(provisioner, swarmOptions, provisioner.AuthOptions)
	return err
}
