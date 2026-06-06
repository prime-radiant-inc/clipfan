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
	SharedKey         func() (string, error)
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
	sharedKey, err := sshProvisionDirectSharedKey(opts.SharedKey)
	if err != nil {
		return err
	}
	runner := opts.Runner
	if runner == nil {
		runner = sshprovision.ExecCommandRunner{MaxOutputBytes: 64 * 1024}
	}
	ctx := context.Background()
	hosts, confirmedHostKeys, err := scanSSHProvisionDirectHostKeys(ctx, runner, hosts)
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
				Local:     hosts[i],
				Remote:    hosts[j],
				SharedKey: sharedKey,
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

func sshProvisionDirectSharedKey(loader func() (string, error)) (string, error) {
	if loader != nil {
		key, err := loader()
		if err != nil {
			return "", err
		}
		if !config.SharedKeyIsStandard32Bytes(key) {
			return "", fmt.Errorf("invalid_local_shared_key")
		}
		return key, nil
	}
	cfg, err := config.Load()
	if err != nil {
		return "", err
	}
	if !config.SharedKeyIsStandard32Bytes(cfg.SharedKey) {
		return "", fmt.Errorf("invalid_local_shared_key")
	}
	return cfg.SharedKey, nil
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

func scanSSHProvisionDirectHostKeys(ctx context.Context, runner sshprovision.CommandRunner, hosts []sshprovision.DirectPairProvisionHost) ([]sshprovision.DirectPairProvisionHost, map[string]string, error) {
	out := make(map[string]string, len(hosts))
	resolvedHosts := make([]sshprovision.DirectPairProvisionHost, 0, len(hosts))
	for _, host := range hosts {
		keyscanTarget, err := resolveSSHProvisionDirectKeyscanTarget(ctx, runner, host.Host)
		if err != nil {
			return nil, nil, err
		}
		adminHost := host.Host
		host.AdminHost = adminHost
		host.Host.SSHHost = keyscanTarget.Host
		host.Host.SSHPort = keyscanTarget.Port
		command, err := sshprovision.SSHKeyscanCommand(sshprovision.SSHKeyscanSpec{
			Host: keyscanTarget.Host,
			Port: keyscanTarget.Port,
		})
		if err != nil {
			return nil, nil, err
		}
		output, err := runner.Run(ctx, command)
		if err != nil {
			return nil, nil, err
		}
		if output.StdoutTruncated {
			return nil, nil, fmt.Errorf("ssh_keyscan_output_truncated: %s", host.Host.ID)
		}
		line, err := selectSSHProvisionDirectHostKeyLine(host.Host, keyscanTarget, string(output.Stdout))
		if err != nil {
			return nil, nil, err
		}
		out[host.Host.ID] = line
		resolvedHosts = append(resolvedHosts, host)
	}
	return resolvedHosts, out, nil
}

type sshProvisionDirectKeyscanTarget struct {
	Host string
	Port int
}

func resolveSSHProvisionDirectKeyscanTarget(ctx context.Context, runner sshprovision.CommandRunner, host sshprovision.DirectPairHost) (sshProvisionDirectKeyscanTarget, error) {
	command, err := sshprovision.RegularSSHConfigCommand(sshprovision.SSHConfigSpec{
		User: host.SSHUser,
		Host: host.SSHHost,
		Port: host.SSHPort,
	})
	if err != nil {
		return sshProvisionDirectKeyscanTarget{}, err
	}
	output, err := runner.Run(ctx, command)
	if err != nil {
		return sshProvisionDirectKeyscanTarget{}, err
	}
	if output.StdoutTruncated {
		return sshProvisionDirectKeyscanTarget{}, fmt.Errorf("ssh_config_output_truncated: %s", host.ID)
	}
	return parseSSHProvisionDirectKeyscanTarget(host, string(output.Stdout))
}

func parseSSHProvisionDirectKeyscanTarget(host sshprovision.DirectPairHost, output string) (sshProvisionDirectKeyscanTarget, error) {
	target := sshProvisionDirectKeyscanTarget{Host: host.SSHHost, Port: host.SSHPort}
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		value = strings.TrimSpace(value)
		switch key {
		case "hostname":
			canonical, err := config.CanonicalSSHHost(value)
			if err != nil {
				return sshProvisionDirectKeyscanTarget{}, fmt.Errorf("invalid_ssh_config_hostname: %s: %w", host.ID, err)
			}
			target.Host = canonical
		case "port":
			parsed, err := strconv.Atoi(value)
			if err != nil || parsed < 1 || parsed > 65535 {
				return sshProvisionDirectKeyscanTarget{}, fmt.Errorf("invalid_ssh_config_port: %s", host.ID)
			}
			target.Port = parsed
		case "proxycommand", "proxyjump":
			if value != "" && value != "none" {
				return sshProvisionDirectKeyscanTarget{}, fmt.Errorf("unsupported_ssh_config_for_keyscan: %s: %s", host.ID, key)
			}
		case "hostkeyalias":
			if value != "" && value != "none" {
				return sshProvisionDirectKeyscanTarget{}, fmt.Errorf("unsupported_ssh_config_for_keyscan: %s: %s", host.ID, key)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return sshProvisionDirectKeyscanTarget{}, err
	}
	return target, nil
}

func selectSSHProvisionDirectHostKeyLine(host sshprovision.DirectPairHost, target sshProvisionDirectKeyscanTarget, output string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(output))
	selectedLine := ""
	selectedRank := len(sshProvisionDirectHostKeyPreference) + 1
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		pin, err := sshprovision.ParseKnownHostScanLine(target.Host, target.Port, line)
		if err != nil {
			continue
		}
		retargeted, err := sshprovision.NewKnownHostPin(host.SSHHost, host.SSHPort, pin.KeyType, pin.PublicKey)
		if err != nil {
			return "", err
		}
		rank := sshProvisionDirectHostKeyRank(retargeted.KeyType)
		if selectedLine == "" || rank < selectedRank {
			selectedLine = retargeted.Line()
			selectedRank = rank
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	if selectedLine != "" {
		return selectedLine, nil
	}
	return "", fmt.Errorf("ssh_keyscan_no_matching_host_key: %s", host.ID)
}

var sshProvisionDirectHostKeyPreference = []string{
	"ssh-ed25519",
	"sk-ssh-ed25519@openssh.com",
	"ecdsa-sha2-nistp256",
	"ecdsa-sha2-nistp384",
	"ecdsa-sha2-nistp521",
	"sk-ecdsa-sha2-nistp256@openssh.com",
	"ssh-rsa",
}

func sshProvisionDirectHostKeyRank(keyType string) int {
	for i, preferred := range sshProvisionDirectHostKeyPreference {
		if keyType == preferred {
			return i
		}
	}
	return len(sshProvisionDirectHostKeyPreference)
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
