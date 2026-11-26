// Package ec2 configures a sshdriver.Provider for EC2-based load generators
// provisioned via OpenTofu. All cloud-agnostic bootstrap logic (tofu
// apply/destroy, SSH/SCP bootstrap, benchctl resume handoff) lives in
// internal/providers/sshdriver; this package only supplies EC2-specific
// defaults.
package ec2

import (
	"regexp"
	"strings"

	"github.com/dbarena/benchctl/internal/config"
	"github.com/dbarena/benchctl/internal/providers/sshdriver"
	"github.com/dbarena/benchctl/internal/schema"
)

// gravitonRe matches EC2 instance family names that use Graviton (ARM64) processors.
// Graviton families embed 'g' immediately after the generation digit, e.g. t4g, c8gd, m7g.
var gravitonRe = regexp.MustCompile(`[0-9]+g`)

// New constructs a driver Provider for EC2-based load generators.
func New(cfg *config.Config) *sshdriver.Provider {
	return sshdriver.New(sshdriver.Config{
		ProviderName:         "ec2",
		DefaultSSHUser:       "ubuntu",
		ArchFromInstanceType: archFromInstanceType,
		Benchctl:             cfg,
	})
}

// NewOnTarget constructs a driver Provider that runs the workload on the
// target instance itself rather than on an instance of its own, so the load
// generator shares the machine with the database and reaches it without a
// network hop. The scenario points it at the target through the driver
// config:
//
//	driver:
//	  provider: ec2-target
//	  config:
//	    host: "{{ target.outputs.public_ip }}"
//	    ssh_private_key_path: "{{ target.outputs.ssh_private_key_path }}"
//	    instance_type: "{{ inputs.target_instance_type }}"
//
// The target module has to install whatever the workload needs (e.g.
// pgbench_enabled in deployments/ec2/postgres), and the suite must not reboot
// the target: that would kill the run driving it.
func NewOnTarget(cfg *config.Config) *sshdriver.Provider {
	return sshdriver.New(sshdriver.Config{
		ProviderName:         ProviderNameOnTarget,
		DefaultSSHUser:       "ubuntu",
		ArchFromInstanceType: archFromInstanceType,
		Benchctl:             cfg,
		ExistingHost:         true,
	})
}

// ProviderNameOnTarget is the scenario's driver.provider for NewOnTarget.
const ProviderNameOnTarget = schema.DriverProviderEC2Target

// archFromInstanceType returns "arm64" for Graviton (ARM) instance families and
// "amd64" for all others. Graviton families embed 'g' immediately after the
// generation digit (e.g. t4g, c8gd, m7g).
func archFromInstanceType(instanceType string) string {
	family := strings.SplitN(instanceType, ".", 2)[0]
	if gravitonRe.MatchString(family) {
		return "arm64"
	}
	return "amd64"
}
