package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/prime-radiant-inc/clipfan/internal/config"
	"github.com/prime-radiant-inc/clipfan/internal/releaseflags"
	"github.com/prime-radiant-inc/clipfan/internal/sshprovision"
)

type sshProvisionDirectHostFlag []string

func (f *sshProvisionDirectHostFlag) String() string {
	return strings.Join(*f, ";")
}

func (f *sshProvisionDirectHostFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("empty host spec")
	}
	*f = append(*f, value)
	return nil
}

type sshProvisionDirectOptions struct {
	Runner            sshprovision.CommandRunner
	ConfigV2WriteGate func() bool
}

func RunSSHProvisionDirect(args []string, stdout io.Writer, stderr io.Writer) error {
	return runSSHProvisionDirect(args, stdout, stderr, sshProvisionDirectOptions{})
}

func runSSHProvisionDirect(args []string, stdout io.Writer, stderr io.Writer, opts sshProvisionDirectOptions) error {
	fs := flag.NewFlagSet("ssh-provision-direct", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var hostSpecs sshProvisionDirectHostFlag
	fs.Var(&hostSpecs, "host", "host spec")
	regularKnownHosts := fs.String("regular-known-hosts", defaultRegularKnownHostsPath(), "known_hosts for regular SSH")
	trustKeyscan := fs.Bool("trust-keyscan", false, "trust ssh-keyscan output for sync pins")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected ssh-provision-direct argument")
	}
	if !*trustKeyscan {
		return fmt.Errorf("trust_keyscan_required")
	}
	if len(hostSpecs) < 2 {
		return fmt.Errorf("ssh_provision_direct_requires_at_least_two_hosts")
	}
	if err := config.ValidateSSHExecutablePath(*regularKnownHosts); err != nil {
		return fmt.Errorf("invalid regular known_hosts path: %w", err)
	}

	hosts, configPaths, err := parseSSHProvisionDirectHosts(hostSpecs)
	if err != nil {
		return err
	}
	if !sshProvisionDirectConfigV2WritesEnabled(opts.ConfigV2WriteGate) {
		return config.ErrConfigV2WritesDisabled
	}
	runner := opts.Runner
	if runner == nil {
		runner = sshprovision.ExecCommandRunner{MaxOutputBytes: 64 * 1024}
	}
	ctx := context.Background()
	confirmedHostKeys, err := scanSSHProvisionDirectHostKeys(ctx, runner, hosts)
	if err != nil {
		return err
	}
	driver := sshprovision.RegularSSHProvisionDriver{
		Runner:                runner,
		RegularKnownHostsPath: *regularKnownHosts,
		ConfirmedHostKeyLines: confirmedHostKeys,
		ProvisionHosts:        provisionHostsByID(hosts),
		ConfigPathByHostID:    configPaths,
	}
	provisioner := sshprovision.NewDirectPairProvisionerWithConfigV2WriteGate(driver, opts.ConfigV2WriteGate)

	var pairs []map[string]any
	for i := 0; i < len(hosts); i++ {
		for j := i + 1; j < len(hosts); j++ {
			result, err := provisioner.Provision(ctx, sshprovision.DirectPairProvisionInput{
				Local:  hosts[i],
				Remote: hosts[j],
			})
			if err != nil {
				return err
			}
			pairs = append(pairs, sshProvisionDirectPairPayload(result))
		}
	}
	return json.NewEncoder(stdout).Encode(map[string]any{
		"status": "ok",
		"pairs":  pairs,
	})
}

func sshProvisionDirectConfigV2WritesEnabled(gate func() bool) bool {
	if gate != nil {
		return gate()
	}
	return releaseflags.ConfigV2WriteEnabled
}

func defaultRegularKnownHostsPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".ssh", "known_hosts")
}

func parseSSHProvisionDirectHosts(specs []string) ([]sshprovision.DirectPairProvisionHost, map[string]string, error) {
	hosts := make([]sshprovision.DirectPairProvisionHost, 0, len(specs))
	configPaths := make(map[string]string, len(specs))
	seen := map[string]struct{}{}
	for _, spec := range specs {
		host, configPath, err := parseSSHProvisionDirectHost(spec)
		if err != nil {
			return nil, nil, err
		}
		hostID := host.Host.ID
		if _, ok := seen[hostID]; ok {
			return nil, nil, fmt.Errorf("duplicate host id: %s", hostID)
		}
		seen[hostID] = struct{}{}
		hosts = append(hosts, host)
		configPaths[hostID] = configPath
	}
	return hosts, configPaths, nil
}

func provisionHostsByID(hosts []sshprovision.DirectPairProvisionHost) map[string]sshprovision.DirectPairProvisionHost {
	out := make(map[string]sshprovision.DirectPairProvisionHost, len(hosts))
	for _, host := range hosts {
		out[host.Host.ID] = host
	}
	return out
}

func parseSSHProvisionDirectHost(spec string) (sshprovision.DirectPairProvisionHost, string, error) {
	fields := map[string]string{}
	for _, item := range strings.Split(spec, ",") {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			return sshprovision.DirectPairProvisionHost{}, "", fmt.Errorf("invalid host spec field: %s", item)
		}
		key = strings.ReplaceAll(strings.TrimSpace(key), "-", "_")
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return sshprovision.DirectPairProvisionHost{}, "", fmt.Errorf("invalid host spec field: %s", item)
		}
		fields[key] = value
	}
	port := 22
	if fields["port"] != "" {
		parsed, err := strconv.Atoi(fields["port"])
		if err != nil {
			return sshprovision.DirectPairProvisionHost{}, "", fmt.Errorf("invalid host port: %w", err)
		}
		port = parsed
	}
	gatewayPath := fields["gateway"]
	if gatewayPath == "" {
		gatewayPath = fields["install"]
	}
	configPath := fields["config"]
	if configPath == "" {
		return sshprovision.DirectPairProvisionHost{}, "", fmt.Errorf("missing host config path")
	}
	if err := config.ValidateSafeAbsolutePath(configPath); err != nil {
		return sshprovision.DirectPairProvisionHost{}, "", fmt.Errorf("invalid host config path: %w", err)
	}
	host := sshprovision.DirectPairProvisionHost{
		Host: sshprovision.DirectPairHost{
			ID:          fields["id"],
			SSHHost:     firstNonEmpty(fields["ssh"], fields["host"]),
			SSHUser:     fields["user"],
			SSHPort:     port,
			InstallPath: fields["install"],
			GatewayPath: gatewayPath,
		},
		KnownHostsPath: firstNonEmpty(fields["known_hosts"], fields["knownhosts"]),
		SyncKeyPath:    firstNonEmpty(fields["sync_key"], fields["synckey"]),
	}
	if err := validateSingleDirectProvisionHost(host); err != nil {
		return sshprovision.DirectPairProvisionHost{}, "", err
	}
	return host, configPath, nil
}

func validateSingleDirectProvisionHost(host sshprovision.DirectPairProvisionHost) error {
	_, err := sshprovision.BuildDirectPairPlan(sshprovision.DirectPairPlanInput{
		Local:  host.Host,
		Remote: otherValidationHost(host.Host.ID),
	})
	if err == nil {
		if err := config.ValidateSSHExecutablePath(host.KnownHostsPath); err != nil {
			return fmt.Errorf("invalid known_hosts path: %w", err)
		}
		if err := config.ValidateSyncKeyPath(host.SyncKeyPath); err != nil {
			return fmt.Errorf("invalid sync key path: %w", err)
		}
		if err := config.ValidateSSHExecutablePath(host.SyncKeyPath); err != nil {
			return fmt.Errorf("invalid sync key ssh path: %w", err)
		}
		return nil
	}
	return fmt.Errorf("invalid host spec: %w", err)
}

func otherValidationHost(id string) sshprovision.DirectPairHost {
	otherID := "validation-peer"
	if id == otherID {
		otherID = "validation-peer-2"
	}
	return sshprovision.DirectPairHost{
		ID:          otherID,
		SSHHost:     "validation-peer.invalid",
		SSHUser:     "validation",
		SSHPort:     22,
		InstallPath: "/tmp/clipfan",
		GatewayPath: "/tmp/clipfan",
	}
}

func scanSSHProvisionDirectHostKeys(ctx context.Context, runner sshprovision.CommandRunner, hosts []sshprovision.DirectPairProvisionHost) (map[string]string, error) {
	out := make(map[string]string, len(hosts))
	for _, host := range hosts {
		command, err := sshprovision.SSHKeyscanCommand(sshprovision.SSHKeyscanSpec{
			Host: host.Host.SSHHost,
			Port: host.Host.SSHPort,
		})
		if err != nil {
			return nil, err
		}
		output, err := runner.Run(ctx, command)
		if err != nil {
			return nil, err
		}
		if output.StdoutTruncated {
			return nil, fmt.Errorf("ssh_keyscan_output_truncated: %s", host.Host.ID)
		}
		line, err := selectSSHProvisionDirectHostKeyLine(host.Host, string(output.Stdout))
		if err != nil {
			return nil, err
		}
		out[host.Host.ID] = line
	}
	return out, nil
}

func selectSSHProvisionDirectHostKeyLine(host sshprovision.DirectPairHost, output string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if _, err := sshprovision.ParseKnownHostScanLine(host.SSHHost, host.SSHPort, line); err == nil {
			return line, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("ssh_keyscan_no_matching_host_key: %s", host.ID)
}

func sshProvisionDirectPairPayload(result sshprovision.DirectPairProvisionResult) map[string]any {
	steps := make([]string, 0, len(result.CompletedSteps))
	for _, step := range result.CompletedSteps {
		steps = append(steps, string(step))
	}
	return map[string]any{
		"pair_id":         result.Plan.PairID,
		"connect_host_id": result.Plan.ConnectHostID,
		"accept_host_id":  result.Plan.AcceptHostID,
		"completed_steps": steps,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
