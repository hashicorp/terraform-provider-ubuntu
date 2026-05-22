package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	ubuntuufwcontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/ubuntuufw"
)

const ufwManagedCommentPrefix = "tf-linux-provider:name="

var (
	ufwNamePattern   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	ufwPortPattern   = regexp.MustCompile(`^[0-9]+(-[0-9]+)?$`)
	ufwNumberPattern = regexp.MustCompile(`^\[\s*(\d+)\]\s+(.+)$`)
)

type ufwRuleResource struct{}

type ufwRuleSpec struct {
	Name               string
	Action             string
	Direction          string
	From               string
	To                 string
	Port               string
	Protocol           string
	Ensure             string
	AllowSSHDisconnect bool
	RuleComment        string
}

type ufwManagedRule struct {
	Number  int
	RawLine string
	Comment string
}

type ufwStatusRule struct {
	Number    int
	Port      string
	Protocol  string
	Action    string
	Direction string
	Source    string
	Comment   string
}

func (r *ufwRuleResource) Name() string { return "ufw_rule" }

func (r *ufwRuleResource) Schema() pluginsdk.Schema {
	return ubuntuufwcontract.RuleResourceSchema()
}

func (r *ufwRuleResource) Validate(config pluginsdk.StateData) error {
	if err := ensureUFWAvailable(); err != nil {
		return err
	}
	_, err := desiredUFWRuleSpec(config)
	return err
}

func (r *ufwRuleResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureUFWAvailable(); err != nil {
		return nil, err
	}

	spec, err := desiredUFWRuleSpec(state)
	if err != nil {
		return nil, err
	}

	rules, err := listManagedUFWRules()
	if err != nil {
		return nil, err
	}
	for _, rule := range rules {
		if rule.Comment == spec.RuleComment {
			return ufwState(spec), nil
		}
	}
	if spec.Ensure == "absent" {
		return ufwAbsentState(spec), nil
	}
	return nil, nil
}

func (r *ufwRuleResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyUFWRule(nil, plan)
}

func (r *ufwRuleResource) Update(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	return applyUFWRule(prior, plan)
}

func (r *ufwRuleResource) Delete(state pluginsdk.StateData) error {
	if err := ensureUFWAvailable(); err != nil {
		return err
	}
	spec, err := desiredUFWRuleSpec(state)
	if err != nil {
		return err
	}
	if err := preventUFWSSHDisconnect(spec, nil); err != nil {
		return err
	}
	return removeManagedUFWRules(spec.RuleComment)
}

func (r *ufwRuleResource) ImportState(string) (pluginsdk.StateData, error) {
	return nil, fmt.Errorf("ufw_rule import is not supported yet; reconcile managed rules by name through terraform configuration")
}

func applyUFWRule(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	if err := ensureUFWAvailable(); err != nil {
		return nil, err
	}

	spec, err := desiredUFWRuleSpec(plan)
	if err != nil {
		return nil, err
	}

	var priorSpec *ufwRuleSpec
	if prior != nil {
		if parsed, parseErr := desiredUFWRuleSpec(prior); parseErr == nil {
			priorSpec = parsed
		}
	}
	if err := preventUFWSSHDisconnect(priorSpec, spec); err != nil {
		return nil, err
	}

	commentsToRemove := []string{spec.RuleComment}
	if priorSpec != nil {
		if priorSpec.RuleComment != spec.RuleComment {
			commentsToRemove = append(commentsToRemove, priorSpec.RuleComment)
		}
	}
	for _, comment := range commentsToRemove {
		if strings.TrimSpace(comment) == "" {
			continue
		}
		if err := removeManagedUFWRules(comment); err != nil {
			return nil, err
		}
	}

	if spec.Ensure == "absent" {
		return ufwAbsentState(spec), nil
	}

	args := []string{spec.Action, spec.Direction, "from", spec.From, "to", spec.To, "port", spec.Port, "proto", spec.Protocol, "comment", spec.RuleComment}
	result, err := pluginsdk.CmdExec("ufw", args)
	if err != nil {
		return nil, fmt.Errorf("apply UFW rule %q: %w", spec.Name, err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("apply UFW rule %q failed (%s)", spec.Name, commandFailureDetail(result))
	}

	return ufwState(spec), nil
}

func desiredUFWRuleSpec(data pluginsdk.StateData) (*ufwRuleSpec, error) {
	name := strings.TrimSpace(data.GetString("name"))
	if !ufwNamePattern.MatchString(name) {
		return nil, fmt.Errorf("name must match %s, got %q", ufwNamePattern.String(), name)
	}

	ensure := withDefault(strings.ToLower(strings.TrimSpace(data.GetString("ensure"))), "present")
	if ensure != "present" && ensure != "absent" {
		return nil, fmt.Errorf("ensure must be \"present\" or \"absent\", got %q", ensure)
	}

	action := withDefault(strings.ToLower(strings.TrimSpace(data.GetString("action"))), "allow")
	if !containsString([]string{"allow", "deny", "reject", "limit"}, action) {
		return nil, fmt.Errorf("action must be one of allow, deny, reject, or limit, got %q", action)
	}

	direction := withDefault(strings.ToLower(strings.TrimSpace(data.GetString("direction"))), "in")
	if !containsString([]string{"in", "out"}, direction) {
		return nil, fmt.Errorf("direction must be \"in\" or \"out\", got %q", direction)
	}

	from := withDefault(strings.TrimSpace(data.GetString("from")), "any")
	to := withDefault(strings.TrimSpace(data.GetString("to")), "any")
	port := strings.TrimSpace(data.GetString("port"))
	if !ufwPortPattern.MatchString(port) {
		return nil, fmt.Errorf("port must be a single port or port range, got %q", port)
	}

	protocol := withDefault(strings.ToLower(strings.TrimSpace(data.GetString("protocol"))), "tcp")
	if !containsString([]string{"tcp", "udp"}, protocol) {
		return nil, fmt.Errorf("protocol must be \"tcp\" or \"udp\", got %q", protocol)
	}

	return &ufwRuleSpec{
		Name:               name,
		Action:             action,
		Direction:          direction,
		From:               from,
		To:                 to,
		Port:               port,
		Protocol:           protocol,
		Ensure:             ensure,
		AllowSSHDisconnect: data.GetBool("allow_ssh_disconnect"),
		RuleComment:        ufwManagedComment(name),
	}, nil
}

func preventUFWSSHDisconnect(prior, plan *ufwRuleSpec) error {
	if plan != nil && plan.AllowSSHDisconnect {
		return nil
	}
	if plan == nil && prior != nil && prior.AllowSSHDisconnect {
		return nil
	}

	active, protectiveIncoming, err := ufwProtectionMode()
	if err != nil {
		return err
	}
	if !active {
		return nil
	}

	bindings := pluginsdk.DiscoverActiveSSHBindings()
	if len(bindings) == 0 {
		return nil
	}

	if plan != nil && plan.Ensure != "absent" && ufwRuleBroadlyBlocksSSH(plan, bindings) {
		return fmt.Errorf("refusing to install a UFW rule that blocks the active SSH listener; set allow_ssh_disconnect=true to override")
	}

	if !protectiveIncoming {
		return nil
	}

	rules, err := listUFWStatusRules()
	if err != nil {
		return err
	}
	currentAllowCounts := countUFWSSHAllowRules(rules, bindings)
	plannedAllowCounts := cloneStringIntMap(currentAllowCounts)

	if prior != nil {
		decrementManagedUFWSSHAllowRules(plannedAllowCounts, rules, prior, bindings)
	}
	if plan != nil && plan.Ensure != "absent" {
		incrementPlannedUFWSSHAllowRules(plannedAllowCounts, plan, bindings)
	}

	for _, binding := range bindings {
		key := binding.Port + "/" + binding.Protocol
		if currentAllowCounts[key] > 0 && plannedAllowCounts[key] == 0 {
			return fmt.Errorf("refusing to remove the last inbound UFW SSH allow rule for active SSH port %s/%s; set allow_ssh_disconnect=true to override", binding.Port, binding.Protocol)
		}
	}

	return nil
}

func ensureUFWAvailable() error {
	exists, err := pluginsdk.CmdExists("ufw")
	if err != nil {
		return fmt.Errorf("check ufw availability: %w", err)
	}
	if !exists {
		return fmt.Errorf("ufw command is required on the host before managing ufw_rule resources")
	}
	return nil
}

func removeManagedUFWRules(comment string) error {
	rules, err := listManagedUFWRules()
	if err != nil {
		return err
	}
	matching := make([]ufwManagedRule, 0)
	for _, rule := range rules {
		if rule.Comment == comment {
			matching = append(matching, rule)
		}
	}
	sort.Slice(matching, func(i, j int) bool {
		return matching[i].Number > matching[j].Number
	})
	for _, rule := range matching {
		result, err := pluginsdk.CmdExec("ufw", []string{"--force", "delete", strconv.Itoa(rule.Number)})
		if err != nil {
			return fmt.Errorf("delete managed UFW rule %q: %w", comment, err)
		}
		if result.ExitCode != 0 {
			return fmt.Errorf("delete managed UFW rule %q failed (%s)", comment, commandFailureDetail(result))
		}
	}
	return nil
}

func listUFWStatusRules() ([]ufwStatusRule, error) {
	result, err := pluginsdk.CmdExec("ufw", []string{"status", "numbered"})
	if err != nil {
		return nil, fmt.Errorf("list UFW rules: %w", err)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("list UFW rules failed (%s)", commandFailureDetail(result))
	}
	return parseAllUFWStatusRules(result.Stdout), nil
}

func listManagedUFWRules() ([]ufwManagedRule, error) {
	rules, err := listUFWStatusRules()
	if err != nil {
		return nil, err
	}
	managed := make([]ufwManagedRule, 0, len(rules))
	for _, rule := range rules {
		if !strings.HasPrefix(rule.Comment, ufwManagedCommentPrefix) {
			continue
		}
		managed = append(managed, ufwManagedRule{Number: rule.Number, Comment: rule.Comment})
	}
	return managed, nil
}

func parseUFWStatusNumbered(raw string) []ufwManagedRule {
	rules := parseAllUFWStatusRules(raw)
	managed := make([]ufwManagedRule, 0, len(rules))
	for _, rule := range rules {
		if !strings.HasPrefix(rule.Comment, ufwManagedCommentPrefix) {
			continue
		}
		managed = append(managed, ufwManagedRule{Number: rule.Number, Comment: rule.Comment})
	}
	return managed
}

func parseAllUFWStatusRules(raw string) []ufwStatusRule {
	rules := make([]ufwStatusRule, 0)
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		matches := ufwNumberPattern.FindStringSubmatch(line)
		if len(matches) != 3 {
			continue
		}
		number, err := strconv.Atoi(matches[1])
		if err != nil {
			continue
		}
		body := matches[2]
		comment := parseUFWComment(body)
		commentless := strings.TrimSpace(strings.SplitN(body, "#", 2)[0])
		fields := strings.Fields(commentless)
		if len(fields) < 4 {
			continue
		}
		port, protocol := parseUFWTarget(fields[0])
		rules = append(rules, ufwStatusRule{
			Number:    number,
			Port:      port,
			Protocol:  protocol,
			Action:    strings.ToLower(fields[1]),
			Direction: strings.ToLower(fields[2]),
			Source:    strings.Join(fields[3:], " "),
			Comment:   comment,
		})
	}
	return rules
}

func parseUFWTarget(target string) (string, string) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", ""
	}
	if before, after, ok := strings.Cut(target, "/"); ok {
		return before, strings.ToLower(after)
	}
	return target, ""
}

func parseUFWComment(line string) string {
	index := strings.Index(line, "#")
	if index < 0 {
		return ""
	}
	return strings.TrimSpace(line[index+1:])
}

func ufwManagedComment(name string) string {
	return ufwManagedCommentPrefix + name
}

func ufwState(spec *ufwRuleSpec) pluginsdk.StateData {
	return pluginsdk.StateData{
		"id":                   spec.Name,
		"name":                 spec.Name,
		"action":               spec.Action,
		"direction":            spec.Direction,
		"from":                 spec.From,
		"to":                   spec.To,
		"port":                 spec.Port,
		"protocol":             spec.Protocol,
		"ensure":               spec.Ensure,
		"allow_ssh_disconnect": spec.AllowSSHDisconnect,
		"rule_comment":         spec.RuleComment,
	}
}

func ufwAbsentState(spec *ufwRuleSpec) pluginsdk.StateData {
	state := ufwState(spec)
	state["ensure"] = "absent"
	return state
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func ufwProtectionMode() (bool, bool, error) {
	result, err := pluginsdk.CmdExec("ufw", []string{"status", "verbose"})
	if err != nil {
		return false, false, fmt.Errorf("inspect UFW status: %w", err)
	}
	if result.ExitCode != 0 {
		return false, false, fmt.Errorf("inspect UFW status failed (%s)", commandFailureDetail(result))
	}
	status := strings.ToLower(result.Stdout)
	active := strings.Contains(status, "status: active")
	protectiveIncoming := strings.Contains(status, "default: deny (incoming)") || strings.Contains(status, "default: reject (incoming)")
	return active, protectiveIncoming, nil
}

func countUFWSSHAllowRules(rules []ufwStatusRule, bindings []pluginsdk.SSHBinding) map[string]int {
	counts := make(map[string]int, len(bindings))
	for _, binding := range bindings {
		key := binding.Port + "/" + binding.Protocol
		for _, rule := range rules {
			if ufwStatusRuleAllowsSSHBinding(rule, binding) {
				counts[key]++
			}
		}
	}
	return counts
}

func decrementManagedUFWSSHAllowRules(counts map[string]int, rules []ufwStatusRule, prior *ufwRuleSpec, bindings []pluginsdk.SSHBinding) {
	if prior == nil {
		return
	}
	for _, binding := range bindings {
		key := binding.Port + "/" + binding.Protocol
		for _, rule := range rules {
			if rule.Comment != prior.RuleComment {
				continue
			}
			if !ufwStatusRuleAllowsSSHBinding(rule, binding) {
				continue
			}
			if counts[key] > 0 {
				counts[key]--
			}
			break
		}
	}
}

func incrementPlannedUFWSSHAllowRules(counts map[string]int, plan *ufwRuleSpec, bindings []pluginsdk.SSHBinding) {
	if plan == nil {
		return
	}
	for _, binding := range bindings {
		if !ufwRuleBroadlyAllowsSSH(plan, []pluginsdk.SSHBinding{binding}) {
			continue
		}
		key := binding.Port + "/" + binding.Protocol
		counts[key]++
	}
}

func ufwRuleBroadlyAllowsSSH(spec *ufwRuleSpec, bindings []pluginsdk.SSHBinding) bool {
	return ufwRuleBroadlyMatchesSSH(spec, bindings, []string{"allow", "limit"})
}

func ufwRuleBroadlyBlocksSSH(spec *ufwRuleSpec, bindings []pluginsdk.SSHBinding) bool {
	return ufwRuleBroadlyMatchesSSH(spec, bindings, []string{"deny", "reject"})
}

func ufwRuleBroadlyMatchesSSH(spec *ufwRuleSpec, bindings []pluginsdk.SSHBinding, actions []string) bool {
	if spec == nil || spec.Ensure == "absent" || spec.Direction != "in" || !containsString(actions, spec.Action) {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(spec.From), "any") {
		return false
	}
	for _, binding := range bindings {
		if spec.Port != binding.Port {
			continue
		}
		if spec.Protocol != binding.Protocol {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(spec.To), "any") || (binding.LocalAddress != "" && strings.TrimSpace(spec.To) == binding.LocalAddress) {
			return true
		}
	}
	return false
}

func ufwStatusRuleAllowsSSHBinding(rule ufwStatusRule, binding pluginsdk.SSHBinding) bool {
	if rule.Direction != "in" {
		return false
	}
	if rule.Action != "allow" && rule.Action != "limit" {
		return false
	}
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(rule.Source)), "anywhere") {
		return false
	}
	if rule.Port != binding.Port {
		return false
	}
	if rule.Protocol != "" && rule.Protocol != binding.Protocol {
		return false
	}
	return true
}

func cloneStringIntMap(source map[string]int) map[string]int {
	result := make(map[string]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func withDefault(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func commandFailureDetail(result *pluginsdk.CmdResult) string {
	return pluginsdk.CommandOutputDetail(result)
}

func init() {
	pluginsdk.RegisterResource(&ufwRuleResource{})
}

func main() {
	pluginsdk.Run()
}
