package main

import (
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk"
	linuxfilescontract "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/contracts/linuxfiles"
	crontabsdk "github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/crontab"
	"github.com/hashicorp/terraform-provider-ubuntu/guest/sdk/hostfs"
)

const crontabManagedMarkerPrefix = "# tf-linux-provider: crontab_entry name="

type crontabEntryResource struct{}

type cronSchedule struct {
	Canonical  string
	Special    string
	Minute     string
	Hour       string
	DayOfMonth string
	Month      string
	DayOfWeek  string
}

type desiredCrontabEntry struct {
	Name     string
	User     string
	Command  string
	Schedule cronSchedule
	Line     pluginsdk.CrontabLine
}

type managedCrontabBlock struct {
	MarkerIndex int
	JobIndex    int
	HasJob      bool
	Line        pluginsdk.CrontabLine
}

func (r *crontabEntryResource) Name() string { return "crontab_entry" }

func (r *crontabEntryResource) Schema() pluginsdk.Schema {
	return linuxfilescontract.CrontabEntryResourceSchema()
}

func (r *crontabEntryResource) Validate(config pluginsdk.StateData) error {
	_, err := desiredCrontabFromState(config)
	return err
}

func (r *crontabEntryResource) Read(state pluginsdk.StateData) (pluginsdk.StateData, error) {
	name := strings.TrimSpace(state.GetString("name"))
	user := strings.TrimSpace(state.GetString("user"))
	if name == "" || user == "" {
		return nil, nil
	}

	lines, err := readUserCrontabLines(user)
	if err != nil {
		return nil, err
	}

	block, found, err := findManagedCrontabBlock(lines, name)
	if err != nil {
		return nil, err
	}
	if !found || !block.HasJob {
		return nil, nil
	}

	return crontabState(name, user, strings.TrimSpace(block.Line.Command), scheduleFromCrontabLine(block.Line))
}

func (r *crontabEntryResource) Create(plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	desired, err := desiredCrontabFromState(plan)
	if err != nil {
		return nil, err
	}

	lines, err := readUserCrontabLines(desired.User)
	if err != nil {
		return nil, err
	}

	updated, err := createOrAdoptManagedCrontabEntry(lines, desired)
	if err != nil {
		return nil, err
	}
	if err := writeUserCrontabLines(desired.User, updated); err != nil {
		return nil, err
	}

	return desired.State(), nil
}

func (r *crontabEntryResource) Update(prior, plan pluginsdk.StateData) (pluginsdk.StateData, error) {
	desired, err := desiredCrontabFromState(plan)
	if err != nil {
		return nil, err
	}

	priorName := strings.TrimSpace(prior.GetString("name"))
	priorUser := strings.TrimSpace(prior.GetString("user"))
	if priorName == "" || priorUser == "" {
		return nil, fmt.Errorf("prior state is missing required crontab identity")
	}

	if priorUser == desired.User {
		lines, err := readUserCrontabLines(priorUser)
		if err != nil {
			return nil, err
		}

		var updated []pluginsdk.CrontabLine
		if priorName == desired.Name {
			updated, err = updateManagedCrontabEntry(lines, desired.Name, desired.Line)
			if err != nil && strings.Contains(err.Error(), "not found during update") {
				updated, err = createOrAdoptManagedCrontabEntry(lines, desired)
			}
		} else {
			var removed bool
			lines, removed, err = removeManagedCrontabEntry(lines, priorName)
			if err != nil {
				return nil, err
			}
			if !removed {
				lines = append([]pluginsdk.CrontabLine{}, lines...)
			}
			updated, err = createOrAdoptManagedCrontabEntry(lines, desired)
		}
		if err != nil {
			return nil, err
		}
		if err := writeUserCrontabLines(desired.User, updated); err != nil {
			return nil, err
		}
		return desired.State(), nil
	}

	oldLines, err := readUserCrontabLines(priorUser)
	if err != nil {
		return nil, err
	}
	prunedOld, removedOld, err := removeManagedCrontabEntry(oldLines, priorName)
	if err != nil {
		return nil, err
	}

	newLines, err := readUserCrontabLines(desired.User)
	if err != nil {
		return nil, err
	}
	updatedNew, err := createOrAdoptManagedCrontabEntry(newLines, desired)
	if err != nil {
		return nil, err
	}

	if err := writeUserCrontabLines(desired.User, updatedNew); err != nil {
		return nil, err
	}
	if removedOld {
		if err := writeUserCrontabLines(priorUser, prunedOld); err != nil {
			return nil, err
		}
	}

	return desired.State(), nil
}

func (r *crontabEntryResource) Delete(state pluginsdk.StateData) error {
	name := strings.TrimSpace(state.GetString("name"))
	user := strings.TrimSpace(state.GetString("user"))
	if name == "" || user == "" {
		return nil
	}

	lines, err := readUserCrontabLines(user)
	if err != nil {
		return err
	}
	updated, removed, err := removeManagedCrontabEntry(lines, name)
	if err != nil {
		return err
	}
	if !removed {
		return nil
	}

	return writeUserCrontabLines(user, updated)
}

func (r *crontabEntryResource) ImportState(id string) (pluginsdk.StateData, error) {
	parts := strings.SplitN(strings.TrimSpace(id), "/", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return nil, fmt.Errorf("import ID must be in the format \"user/name\", got %q", id)
	}

	state, err := r.Read(pluginsdk.StateData{
		"user": parts[0],
		"name": parts[1],
	})
	if err != nil {
		return nil, err
	}
	if state == nil {
		return nil, fmt.Errorf("managed crontab entry %q for user %q not found", parts[1], parts[0])
	}
	return state, nil
}

func desiredCrontabFromState(state pluginsdk.StateData) (desiredCrontabEntry, error) {
	name := strings.TrimSpace(state.GetString("name"))
	if !isValidCrontabResourceName(name) {
		return desiredCrontabEntry{}, fmt.Errorf("name must match [A-Za-z0-9._-]+, got %q", name)
	}

	user := strings.TrimSpace(state.GetString("user"))
	if user == "" {
		return desiredCrontabEntry{}, fmt.Errorf("user must not be empty")
	}
	if containsLineBreak(user) {
		return desiredCrontabEntry{}, fmt.Errorf("user must not contain newlines")
	}

	command := strings.TrimSpace(state.GetString("command"))
	if command == "" {
		return desiredCrontabEntry{}, fmt.Errorf("command must not be empty")
	}
	if containsLineBreak(command) {
		return desiredCrontabEntry{}, fmt.Errorf("command must not contain newlines")
	}

	schedule, err := scheduleFromConfig(state)
	if err != nil {
		return desiredCrontabEntry{}, err
	}

	desired := desiredCrontabEntry{
		Name:     name,
		User:     user,
		Command:  command,
		Schedule: schedule,
	}
	desired.Line = schedule.Line(command)
	return desired, nil
}

func (d desiredCrontabEntry) ID() string {
	return crontabEntryID(d.User, d.Name)
}

func (d desiredCrontabEntry) State() pluginsdk.StateData {
	state, err := crontabState(d.Name, d.User, d.Command, d.Schedule)
	if err != nil {
		return pluginsdk.StateData{
			"id":       d.ID(),
			"name":     d.Name,
			"user":     d.User,
			"command":  d.Command,
			"schedule": d.Schedule.Canonical,
		}
	}
	return state
}

func scheduleFromConfig(state pluginsdk.StateData) (cronSchedule, error) {
	scheduleText := strings.TrimSpace(state.GetString("schedule"))
	fieldValues := []string{
		strings.TrimSpace(state.GetString("minute")),
		strings.TrimSpace(state.GetString("hour")),
		strings.TrimSpace(state.GetString("day_of_month")),
		strings.TrimSpace(state.GetString("month")),
		strings.TrimSpace(state.GetString("day_of_week")),
	}

	fieldCount := 0
	for _, value := range fieldValues {
		if value != "" {
			fieldCount++
		}
	}

	switch {
	case scheduleText != "" && fieldCount > 0:
		return cronSchedule{}, fmt.Errorf("schedule is mutually exclusive with minute, hour, day_of_month, month, and day_of_week")
	case scheduleText != "":
		return parseCronScheduleString(scheduleText)
	case fieldCount == 0:
		return cronSchedule{}, fmt.Errorf("exactly one schedule form must be configured")
	case fieldCount != 5:
		return cronSchedule{}, fmt.Errorf("minute, hour, day_of_month, month, and day_of_week must all be set together")
	default:
		return parseCronScheduleFields(fieldValues[0], fieldValues[1], fieldValues[2], fieldValues[3], fieldValues[4])
	}
}

func parseCronScheduleString(value string) (cronSchedule, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return cronSchedule{}, fmt.Errorf("schedule must not be empty")
	}
	if strings.HasPrefix(value, "@") {
		fields := strings.Fields(value)
		if len(fields) != 1 {
			return cronSchedule{}, fmt.Errorf("special schedules must be a single @token, got %q", value)
		}
		token := strings.ToLower(fields[0])
		if len(token) < 2 {
			return cronSchedule{}, fmt.Errorf("invalid special schedule %q", value)
		}
		return cronSchedule{Canonical: token, Special: token}, nil
	}

	fields := strings.Fields(value)
	if len(fields) != 5 {
		return cronSchedule{}, fmt.Errorf("schedule must be a five-field cron expression or @special token, got %q", value)
	}
	return parseCronScheduleFields(fields[0], fields[1], fields[2], fields[3], fields[4])
}

func parseCronScheduleFields(minute, hour, dayOfMonth, month, dayOfWeek string) (cronSchedule, error) {
	fields := []string{
		strings.TrimSpace(minute),
		strings.TrimSpace(hour),
		strings.TrimSpace(dayOfMonth),
		strings.TrimSpace(month),
		strings.TrimSpace(dayOfWeek),
	}
	for _, field := range fields {
		if field == "" {
			return cronSchedule{}, fmt.Errorf("cron fields must not be empty")
		}
		if strings.ContainsAny(field, " \t\r\n") {
			return cronSchedule{}, fmt.Errorf("cron fields must not contain whitespace, got %q", field)
		}
	}

	return cronSchedule{
		Canonical:  strings.Join(fields, " "),
		Minute:     fields[0],
		Hour:       fields[1],
		DayOfMonth: fields[2],
		Month:      fields[3],
		DayOfWeek:  fields[4],
	}, nil
}

func (s cronSchedule) Line(command string) pluginsdk.CrontabLine {
	line := pluginsdk.CrontabLine{
		Command: strings.TrimSpace(command),
	}
	if s.Special != "" {
		line.Special = s.Special
		return line
	}
	line.Minute = s.Minute
	line.Hour = s.Hour
	line.DayOfMonth = s.DayOfMonth
	line.Month = s.Month
	line.DayOfWeek = s.DayOfWeek
	return line
}

func scheduleFromCrontabLine(line pluginsdk.CrontabLine) cronSchedule {
	if strings.TrimSpace(line.Special) != "" {
		special := strings.ToLower(strings.TrimSpace(line.Special))
		return cronSchedule{
			Canonical: special,
			Special:   special,
		}
	}
	return cronSchedule{
		Canonical:  strings.Join([]string{strings.TrimSpace(line.Minute), strings.TrimSpace(line.Hour), strings.TrimSpace(line.DayOfMonth), strings.TrimSpace(line.Month), strings.TrimSpace(line.DayOfWeek)}, " "),
		Minute:     strings.TrimSpace(line.Minute),
		Hour:       strings.TrimSpace(line.Hour),
		DayOfMonth: strings.TrimSpace(line.DayOfMonth),
		Month:      strings.TrimSpace(line.Month),
		DayOfWeek:  strings.TrimSpace(line.DayOfWeek),
	}
}

func crontabState(name, user, command string, schedule cronSchedule) (pluginsdk.StateData, error) {
	if name == "" || user == "" || command == "" || schedule.Canonical == "" {
		return nil, fmt.Errorf("missing required crontab state values")
	}

	state := pluginsdk.StateData{
		"id":       crontabEntryID(user, name),
		"name":     name,
		"user":     user,
		"command":  command,
		"schedule": schedule.Canonical,
	}
	if schedule.Special == "" {
		state["minute"] = schedule.Minute
		state["hour"] = schedule.Hour
		state["day_of_month"] = schedule.DayOfMonth
		state["month"] = schedule.Month
		state["day_of_week"] = schedule.DayOfWeek
	}
	return state, nil
}

func createOrAdoptManagedCrontabEntry(lines []pluginsdk.CrontabLine, desired desiredCrontabEntry) ([]pluginsdk.CrontabLine, error) {
	if _, found, err := findManagedCrontabBlock(lines, desired.Name); err != nil {
		return nil, err
	} else if found {
		return nil, importRequiredError("crontab entry", desired.ID())
	}

	matches := exactUnmanagedCrontabMatches(lines, desired.Line)
	if len(matches) > 1 {
		return nil, fmt.Errorf("multiple unmanaged crontab entries already match %q for user %q", desired.Name, desired.User)
	}

	if len(matches) == 1 {
		index := matches[0]
		updated := make([]pluginsdk.CrontabLine, 0, len(lines)+1)
		updated = append(updated, lines[:index]...)
		updated = append(updated, managedCrontabMarker(desired.Name))
		updated = append(updated, lines[index:]...)
		return updated, nil
	}

	updated := append([]pluginsdk.CrontabLine{}, lines...)
	updated = append(updated, managedCrontabMarker(desired.Name), desired.Line)
	return updated, nil
}

func updateManagedCrontabEntry(lines []pluginsdk.CrontabLine, name string, desired pluginsdk.CrontabLine) ([]pluginsdk.CrontabLine, error) {
	block, found, err := findManagedCrontabBlock(lines, name)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("managed crontab entry %q not found during update", name)
	}

	updated := make([]pluginsdk.CrontabLine, 0, len(lines)+1)
	for i := 0; i < len(lines); i++ {
		if i == block.MarkerIndex {
			updated = append(updated, managedCrontabMarker(name), desired)
			if block.HasJob {
				i = block.JobIndex
			}
			continue
		}
		updated = append(updated, lines[i])
	}

	return updated, nil
}

func removeManagedCrontabEntry(lines []pluginsdk.CrontabLine, name string) ([]pluginsdk.CrontabLine, bool, error) {
	block, found, err := findManagedCrontabBlock(lines, name)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return append([]pluginsdk.CrontabLine{}, lines...), false, nil
	}

	updated := make([]pluginsdk.CrontabLine, 0, len(lines))
	for i := 0; i < len(lines); i++ {
		if i == block.MarkerIndex {
			if block.HasJob {
				i = block.JobIndex
			}
			continue
		}
		updated = append(updated, lines[i])
	}

	return updated, true, nil
}

func findManagedCrontabBlock(lines []pluginsdk.CrontabLine, name string) (managedCrontabBlock, bool, error) {
	var block managedCrontabBlock
	found := false
	for i, line := range lines {
		markerName, ok := managedCrontabName(line)
		if !ok || markerName != name {
			continue
		}
		if found {
			return managedCrontabBlock{}, true, fmt.Errorf("multiple managed crontab markers found for %q", name)
		}
		found = true
		block.MarkerIndex = i
		block.JobIndex = -1
		if i+1 < len(lines) && isCrontabJobLine(lines[i+1]) {
			block.HasJob = true
			block.JobIndex = i + 1
			block.Line = lines[i+1]
		}
	}
	return block, found, nil
}

func exactUnmanagedCrontabMatches(lines []pluginsdk.CrontabLine, desired pluginsdk.CrontabLine) []int {
	matches := make([]int, 0)
	for i, line := range lines {
		if !isCrontabJobLine(line) {
			continue
		}
		if i > 0 {
			if _, managed := managedCrontabName(lines[i-1]); managed {
				continue
			}
		}
		if sameCrontabCommand(line, desired) {
			matches = append(matches, i)
		}
	}
	return matches
}

func sameCrontabCommand(left, right pluginsdk.CrontabLine) bool {
	if strings.TrimSpace(left.Command) != strings.TrimSpace(right.Command) {
		return false
	}
	return scheduleFromCrontabLine(left).Canonical == scheduleFromCrontabLine(right).Canonical
}

func managedCrontabMarker(name string) pluginsdk.CrontabLine {
	return pluginsdk.CrontabLine{
		IsComment: true,
		Comment:   crontabManagedMarkerPrefix + name,
	}
}

func managedCrontabName(line pluginsdk.CrontabLine) (string, bool) {
	if !line.IsComment {
		return "", false
	}

	comment := strings.TrimSpace(line.Comment)
	if comment == "" {
		comment = strings.TrimSpace(line.Raw)
	}
	if !strings.HasPrefix(comment, crontabManagedMarkerPrefix) {
		return "", false
	}

	name := strings.TrimSpace(strings.TrimPrefix(comment, crontabManagedMarkerPrefix))
	if !isValidCrontabResourceName(name) {
		return "", false
	}
	return name, true
}

func isCrontabJobLine(line pluginsdk.CrontabLine) bool {
	if strings.TrimSpace(line.Command) == "" {
		return false
	}
	if strings.TrimSpace(line.Special) != "" {
		return true
	}
	return strings.TrimSpace(line.Minute) != "" &&
		strings.TrimSpace(line.Hour) != "" &&
		strings.TrimSpace(line.DayOfMonth) != "" &&
		strings.TrimSpace(line.Month) != "" &&
		strings.TrimSpace(line.DayOfWeek) != ""
}

func readUserCrontabLines(user string) ([]pluginsdk.CrontabLine, error) {
	if err := ensureCrontabCommand(); err != nil {
		return nil, err
	}

	result, err := pluginsdk.CmdExec("crontab", []string{"-u", user, "-l"})
	if err != nil {
		return nil, fmt.Errorf("read crontab for %q: %w", user, err)
	}
	if result.ExitCode != 0 {
		if isNoCrontabResult(result) {
			return nil, nil
		}
		return nil, fmt.Errorf("read crontab for %q failed (exit %d): %s", user, result.ExitCode, crontabResultMessage(result))
	}

	lines, err := crontabsdk.Parse([]byte(stripCrontabReadHeaders(result.Stdout)))
	if err != nil {
		return nil, fmt.Errorf("parse crontab for %q: %w", user, err)
	}
	return lines, nil
}

func writeUserCrontabLines(user string, lines []pluginsdk.CrontabLine) error {
	if err := ensureCrontabCommand(); err != nil {
		return err
	}

	content, err := crontabsdk.Serialize(lines)
	if err != nil {
		return fmt.Errorf("serialize crontab for %q: %w", user, err)
	}
	if strings.TrimSpace(string(content)) == "" {
		return removeUserCrontab(user)
	}

	tmpPath, err := hostfs.WriteTempFile("tf-linux-provider-crontab-"+sanitizeCrontabTempToken(user), "", content, 0o600)
	if err != nil {
		return fmt.Errorf("write temp crontab for %q: %w", user, err)
	}
	defer func() {
		_ = hostfs.CleanupFile(tmpPath)
	}()

	result, err := pluginsdk.CmdExec("crontab", []string{"-u", user, tmpPath})
	if err != nil {
		return fmt.Errorf("install crontab for %q: %w", user, err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("install crontab for %q failed (exit %d): %s", user, result.ExitCode, crontabResultMessage(result))
	}
	return nil
}

func removeUserCrontab(user string) error {
	result, err := pluginsdk.CmdExec("crontab", []string{"-u", user, "-r"})
	if err != nil {
		return fmt.Errorf("remove crontab for %q: %w", user, err)
	}
	if result.ExitCode != 0 && !isNoCrontabResult(result) {
		return fmt.Errorf("remove crontab for %q failed (exit %d): %s", user, result.ExitCode, crontabResultMessage(result))
	}
	return nil
}

func ensureCrontabCommand() error {
	hasCmd, err := pluginsdk.HostHasCommand("crontab")
	if err != nil {
		return fmt.Errorf("check for crontab command: %w", err)
	}
	if !hasCmd {
		return fmt.Errorf("crontab command not found; install a cron implementation such as the Debian/Ubuntu cron package before managing crontab entries")
	}
	return nil
}

func stripCrontabReadHeaders(content string) string {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") && strings.Contains(strings.ToUpper(trimmed), "DO NOT EDIT THIS FILE") {
			continue
		}
		filtered = append(filtered, line)
	}
	return strings.Join(filtered, "\n")
}

func isNoCrontabResult(result *pluginsdk.CmdResult) bool {
	if result == nil {
		return false
	}
	text := strings.ToLower(strings.TrimSpace(result.Stderr + "\n" + result.Stdout))
	return strings.Contains(text, "no crontab for")
}

func crontabResultMessage(result *pluginsdk.CmdResult) string {
	if result == nil {
		return ""
	}
	message := strings.TrimSpace(result.Stderr)
	if message == "" {
		message = strings.TrimSpace(result.Stdout)
	}
	return message
}

func crontabEntryID(user, name string) string {
	return strings.TrimSpace(user) + "/" + strings.TrimSpace(name)
}

func sanitizeCrontabTempToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' {
			b.WriteByte(ch)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}

func isValidCrontabResourceName(value string) bool {
	if value == "" {
		return false
	}
	for i := 0; i < len(value); i++ {
		ch := value[i]
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return false
	}
	return true
}

func containsLineBreak(value string) bool {
	return strings.ContainsAny(value, "\r\n")
}
