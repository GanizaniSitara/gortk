package aws

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file holds the pure filter functions — the heart of each AWS command.
// Each takes the raw JSON (or text) and returns a compressed filterResult.
// JSON filters return (result, ok=false) to signal "fall back to raw output",
// mirroring rtk's Option<FilterResult>. These are the functions the ported
// #[cfg(test)] tests exercise directly.

func filterSTSIdentity(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	account := strOr(get(v, "Account"), "?")
	arn := strOr(get(v, "Arn"), "?")
	return newResult(fmt.Sprintf("AWS: %s %s", account, arn)), true
}

func filterS3Ls(output string) filterResult {
	lines := splitLines(output)
	total := len(lines)
	limit := maxItems + 10

	if total > limit {
		text := fmt.Sprintf("%s\n… +%d more items", strings.Join(lines[:limit], "\n"), total-limit)
		return truncatedResult(text)
	}
	return newResult(strings.Join(lines, "\n"))
}

func filterEC2Instances(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	reservations := asArray(get(v, "Reservations"))
	if reservations == nil {
		return filterResult{}, false
	}

	var instances []string
	for _, res := range reservations {
		insts := asArray(get(res, "Instances"))
		for _, inst := range insts {
			id := strOr(get(inst, "InstanceId"), "?")
			state := strOr(get(inst, "State", "Name"), "?")
			itype := strOr(get(inst, "InstanceType"), "?")
			privateIP := strOr(get(inst, "PrivateIpAddress"), "-")
			publicIP := strOr(get(inst, "PublicIpAddress"), "-")
			subnet := strOr(get(inst, "SubnetId"), "-")
			vpc := strOr(get(inst, "VpcId"), "-")

			name := "-"
			for _, tag := range asArray(get(inst, "Tags")) {
				if strOr(get(tag, "Key"), "") == "Name" {
					if val, ok := asString(get(tag, "Value")); ok {
						name = val
					}
					break
				}
			}

			var sgs []string
			for _, sg := range asArray(get(inst, "SecurityGroups")) {
				if gid, ok := asString(get(sg, "GroupId")); ok {
					sgs = append(sgs, gid)
				}
			}
			sgStr := "-"
			if len(sgs) > 0 {
				sgStr = strings.Join(sgs, ",")
			}

			instances = append(instances, fmt.Sprintf(
				"%s %s %s %s pub:%s vpc:%s subnet:%s sg:[%s] (%s)",
				id, state, itype, privateIP, publicIP, vpc, subnet, sgStr, name,
			))
		}
	}

	total := len(instances)
	truncated := total > maxItems
	var b strings.Builder
	fmt.Fprintf(&b, "EC2: %d instances\n", total)
	for i, inst := range instances {
		if i >= maxItems {
			break
		}
		fmt.Fprintf(&b, "  %s\n", inst)
	}
	if truncated {
		fmt.Fprintf(&b, "  … +%d more\n", total-maxItems)
	}

	text := strings.TrimRight(b.String(), "\n")
	if truncated {
		return truncatedResult(text), true
	}
	return newResult(text), true
}

func filterECSListServices(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	arns := asArray(get(v, "serviceArns"))
	if arns == nil {
		return filterResult{}, false
	}

	total := len(arns)
	var result []string
	for i, arn := range arns {
		if i >= maxItems {
			break
		}
		result = append(result, shortenARN(strOr(arn, "?")))
	}

	text := joinWithOverflow(result, total, maxItems, "services")
	return resultWithOverflow(text, total > maxItems), true
}

func filterECSDescribeServices(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	services := asArray(get(v, "services"))
	if services == nil {
		return filterResult{}, false
	}

	total := len(services)
	var result []string
	for i, svc := range services {
		if i >= maxItems {
			break
		}
		name := strOr(get(svc, "serviceName"), "?")
		status := strOr(get(svc, "status"), "?")
		running := i64Or(get(svc, "runningCount"), 0)
		desired := i64Or(get(svc, "desiredCount"), 0)
		launch := strOr(get(svc, "launchType"), "?")
		result = append(result, fmt.Sprintf("%s %s %d/%d (%s)", name, status, running, desired, launch))
	}

	text := joinWithOverflow(result, total, maxItems, "services")
	return resultWithOverflow(text, total > maxItems), true
}

func filterRDSInstances(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	dbs := asArray(get(v, "DBInstances"))
	if dbs == nil {
		return filterResult{}, false
	}

	total := len(dbs)
	var result []string
	for i, db := range dbs {
		if i >= maxItems {
			break
		}
		name := strOr(get(db, "DBInstanceIdentifier"), "?")
		engine := strOr(get(db, "Engine"), "?")
		version := strOr(get(db, "EngineVersion"), "?")
		class := strOr(get(db, "DBInstanceClass"), "?")
		status := strOr(get(db, "DBInstanceStatus"), "?")
		endpoint := strOr(get(db, "Endpoint", "Address"), "-")
		port := i64Or(get(db, "Endpoint", "Port"), 0)
		result = append(result, fmt.Sprintf("%s %s %s %s %s %s:%d", name, engine, version, class, status, endpoint, port))
	}

	text := joinWithOverflow(result, total, maxItems, "instances")
	return resultWithOverflow(text, total > maxItems), true
}

func filterCFNListStacks(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	stacks := asArray(get(v, "StackSummaries"))
	if stacks == nil {
		return filterResult{}, false
	}

	total := len(stacks)
	var result []string
	for i, stack := range stacks {
		if i >= maxItems {
			break
		}
		name := strOr(get(stack, "StackName"), "?")
		status := strOr(get(stack, "StackStatus"), "?")
		date := stackDate(stack)
		result = append(result, fmt.Sprintf("%s %s %s", name, status, truncateISODate(date)))
	}

	text := joinWithOverflow(result, total, maxItems, "stacks")
	return resultWithOverflow(text, total > maxItems), true
}

func filterCFNDescribeStacks(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	stacks := asArray(get(v, "Stacks"))
	if stacks == nil {
		return filterResult{}, false
	}

	total := len(stacks)
	var result []string
	for i, stack := range stacks {
		if i >= maxItems {
			break
		}
		name := strOr(get(stack, "StackName"), "?")
		status := strOr(get(stack, "StackStatus"), "?")
		date := stackDate(stack)
		result = append(result, fmt.Sprintf("%s %s %s", name, status, truncateISODate(date)))

		for _, out := range asArray(get(stack, "Outputs")) {
			key := strOr(get(out, "OutputKey"), "?")
			val := strOr(get(out, "OutputValue"), "?")
			result = append(result, fmt.Sprintf("  %s=%s", key, val))
		}
	}

	text := joinWithOverflow(result, total, maxItems, "stacks")
	return resultWithOverflow(text, total > maxItems), true
}

// stackDate returns LastUpdatedTime, falling back to CreationTime, then "?".
func stackDate(stack any) string {
	if s, ok := asString(get(stack, "LastUpdatedTime")); ok {
		return s
	}
	if s, ok := asString(get(stack, "CreationTime")); ok {
		return s
	}
	return "?"
}

// --- P0 filters: CloudWatch Logs, CloudFormation Events, Lambda ---

// daysToYMD converts days since the Unix epoch to (year, month, day) on the
// civil calendar (UTC). Algorithm from Howard Hinnant's date algorithms.
func daysToYMD(days int64) (int64, int64, int64) {
	z := days + 719468
	var era int64
	if z >= 0 {
		era = z / 146097
	} else {
		era = (z - 146096) / 146097
	}
	doe := z - era*146097
	yoe := (doe - doe/1460 + doe/36524 - doe/146096) / 365
	y := yoe + era*400
	doy := doe - (365*yoe + yoe/4 - yoe/100)
	mp := (5*doy + 2) / 153
	d := doy - (153*mp+2)/5 + 1
	var m int64
	if mp < 10 {
		m = mp + 3
	} else {
		m = mp - 9
	}
	if m <= 2 {
		y++
	}
	return y, m, d
}

func filterLogsEvents(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	events := asArray(get(v, "events"))
	if events == nil {
		return filterResult{}, false
	}

	total := len(events)
	truncated := total > maxLogEvents
	var lines []string

	for i, event := range events {
		if i >= maxLogEvents {
			break
		}
		// Convert epoch ms to YYYY-MM-DD HH:MM:SS UTC.
		timeStr := "??:??:??"
		if ts, ok := asI64(get(event, "timestamp")); ok && ts > 0 {
			epochSecs := ts / 1000
			days := epochSecs / 86400
			timeOfDay := epochSecs % 86400
			h := timeOfDay / 3600
			mn := (timeOfDay % 3600) / 60
			s := timeOfDay % 60
			y, mo, d := daysToYMD(days)
			timeStr = fmt.Sprintf("%04d-%02d-%02d %02d:%02d:%02d", y, mo, d, h, mn, s)
		}

		msg := strings.TrimRight(strOr(get(event, "message"), ""), " \t\r\n\v\f")
		compactMsg := msg
		if strings.HasPrefix(msg, "{") {
			if mv, ok := parseJSON(msg); ok {
				if c, ok := marshalCompact(mv); ok {
					compactMsg = c
				}
			}
		}

		lines = append(lines, fmt.Sprintf("%s %s", timeStr, compactMsg))
	}

	if truncated {
		lines = append(lines, fmt.Sprintf("… +%d more events", total-maxLogEvents))
	}

	text := strings.Join(lines, "\n")
	return resultWithOverflow(text, truncated), true
}

func filterCFNEvents(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	events := asArray(get(v, "StackEvents"))
	if events == nil {
		return filterResult{}, false
	}

	var failed []string
	failedCount := 0
	successCount := 0

	for _, event := range events {
		status := strOr(get(event, "ResourceStatus"), "?")
		logicalID := strOr(get(event, "LogicalResourceId"), "?")
		resourceTypeRaw := strOr(get(event, "ResourceType"), "?")
		resourceType := strings.TrimPrefix(resourceTypeRaw, "AWS::")
		ts := "?"
		if s, ok := asString(get(event, "Timestamp")); ok {
			ts = truncateISODate(s)
		}

		if strings.Contains(status, "FAILED") || strings.Contains(status, "ROLLBACK") {
			failedCount++
			if len(failed) < maxItems {
				reason := strOr(get(event, "ResourceStatusReason"), "")
				line := fmt.Sprintf("%s %s %s %s", ts, logicalID, resourceType, status)
				if reason != "" {
					line += fmt.Sprintf(" REASON: %s", reason)
				}
				failed = append(failed, line)
			}
		} else {
			successCount++
		}
	}

	totalEvents := len(events)
	var lines []string
	lines = append(lines, fmt.Sprintf(
		"CloudFormation: %d events (%d failed, %d successful)",
		totalEvents, failedCount, successCount,
	))

	if len(failed) > 0 {
		lines = append(lines, "--- FAILURES ---")
		for _, f := range failed {
			lines = append(lines, fmt.Sprintf("  %s", f))
		}
	}

	if successCount > 0 {
		lines = append(lines, fmt.Sprintf("+ %d successful resources", successCount))
	}

	truncated := totalEvents > maxItems*5 // >100 events
	text := strings.Join(lines, "\n")
	return resultWithOverflow(text, truncated), true
}

func filterLambdaList(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	functions := asArray(get(v, "Functions"))
	if functions == nil {
		return filterResult{}, false
	}

	total := len(functions)
	truncated := total > maxItems
	var result []string

	for i, fn := range functions {
		if i >= maxItems {
			break
		}
		name := strOr(get(fn, "FunctionName"), "?")
		runtime := strOr(get(fn, "Runtime"), "?")
		memory := i64Or(get(fn, "MemorySize"), 0)
		timeout := i64Or(get(fn, "Timeout"), 0)
		state := strOr(get(fn, "State"), "active")
		// SECURITY: Environment is intentionally NOT read (may contain secrets).
		result = append(result, fmt.Sprintf("%s %s %dMB %ds %s", name, runtime, memory, timeout, state))
	}

	text := joinWithOverflow(result, total, maxItems, "functions")
	return resultWithOverflow(text, truncated), true
}

func filterLambdaGet(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	config := get(v, "Configuration")

	name := strOr(get(config, "FunctionName"), "?")
	runtime := strOr(get(config, "Runtime"), "?")
	handler := strOr(get(config, "Handler"), "?")
	memory := i64Or(get(config, "MemorySize"), 0)
	timeout := i64Or(get(config, "Timeout"), 0)
	state := strOr(get(config, "State"), "active")
	lastModified := "?"
	if s, ok := asString(get(config, "LastModified")); ok {
		lastModified = truncateISODate(s)
	}
	// SECURITY: Environment and Code.Location intentionally NOT read.

	text := fmt.Sprintf("%s %s %s %dMB %ds %s %s", name, runtime, handler, memory, timeout, state, lastModified)

	// Show layer names if present.
	// Layer ARNs use colons: arn:aws:lambda:region:acct:layer:name:version
	if layers := asArray(get(config, "Layers")); len(layers) > 0 {
		var layerNames []string
		for _, l := range layers {
			arn, ok := asString(get(l, "Arn"))
			if !ok {
				continue
			}
			parts := rsplitN(arn, ':', 3)
			if len(parts) >= 2 {
				layerNames = append(layerNames, fmt.Sprintf("%s:%s", parts[1], parts[0]))
			} else {
				layerNames = append(layerNames, arn)
			}
		}
		text += fmt.Sprintf("\n  layers: %s", strings.Join(layerNames, ", "))
	}

	return newResult(text), true
}

// --- P1 filters: IAM, DynamoDB, ECS tasks ---

// extractAssumePrincipals extracts principal services/accounts from an IAM
// role's AssumeRolePolicyDocument, returning a compact list rather than the
// full policy JSON. Mirrors rtk's extract_assume_principals.
func extractAssumePrincipals(role any) []string {
	var principals []string
	var doc any
	if s, ok := asString(get(role, "AssumeRolePolicyDocument")); ok {
		if parsed, ok := parseJSON(s); ok {
			doc = parsed
		}
	} else if isObject(get(role, "AssumeRolePolicyDocument")) {
		doc = get(role, "AssumeRolePolicyDocument")
	}
	if doc != nil {
		for _, stmt := range asArray(get(doc, "Statement")) {
			principal := get(stmt, "Principal")
			if s, ok := asString(principal); ok {
				principals = append(principals, s)
			} else if svc, ok := asString(get(principal, "Service")); ok {
				principals = append(principals, svc)
			} else if svcs := asArray(get(principal, "Service")); svcs != nil {
				for _, s := range svcs {
					if str, ok := asString(s); ok {
						principals = append(principals, str)
					}
				}
			} else if aws, ok := asString(get(principal, "AWS")); ok {
				principals = append(principals, shortenARN(aws))
			} else if awss := asArray(get(principal, "AWS")); awss != nil {
				for _, a := range awss {
					if str, ok := asString(a); ok {
						principals = append(principals, shortenARN(str))
					}
				}
			}
		}
	}
	return dedupConsecutive(principals)
}

func filterIAMRoles(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	roles := asArray(get(v, "Roles"))
	if roles == nil {
		return filterResult{}, false
	}

	total := len(roles)
	truncated := total > maxItems
	var result []string

	for i, role := range roles {
		if i >= maxItems {
			break
		}
		name := strOr(get(role, "RoleName"), "?")
		date := "?"
		if s, ok := asString(get(role, "CreateDate")); ok {
			date = truncateISODate(s)
		}
		desc := strOr(get(role, "Description"), "")

		principals := extractAssumePrincipals(role)
		principalStr := ""
		if len(principals) > 0 {
			principalStr = fmt.Sprintf(" assume:[%s]", strings.Join(principals, ","))
		}

		if desc == "" {
			result = append(result, fmt.Sprintf("%s %s%s", name, date, principalStr))
		} else {
			result = append(result, fmt.Sprintf("%s %s [%s]%s", name, date, desc, principalStr))
		}
	}

	text := joinWithOverflow(result, total, maxItems, "roles")
	return resultWithOverflow(text, truncated), true
}

func filterIAMUsers(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	users := asArray(get(v, "Users"))
	if users == nil {
		return filterResult{}, false
	}

	total := len(users)
	truncated := total > maxItems
	var result []string

	for i, user := range users {
		if i >= maxItems {
			break
		}
		name := strOr(get(user, "UserName"), "?")
		date := "?"
		if s, ok := asString(get(user, "CreateDate")); ok {
			date = truncateISODate(s)
		}
		result = append(result, fmt.Sprintf("%s created:%s", name, date))
	}

	text := joinWithOverflow(result, total, maxItems, "users")
	return resultWithOverflow(text, truncated), true
}

// unwrapDynamoDBValue recursively unwraps DynamoDB typed values to plain JSON:
// {"S":"foo"}->"foo", {"N":"42"}->42, {"M":{...}}->object, etc. Mirrors rtk's
// unwrap_dynamodb_value.
func unwrapDynamoDBValue(val any, depth int) any {
	if depth > 10 {
		return val
	}

	if obj := asObject(val); obj != nil {
		if obj.len() == 1 {
			key := obj.keys()[0]
			inner := obj.get(key)
			switch key {
			case "S", "B":
				return inner
			case "N":
				if s, ok := asString(inner); ok {
					if n, err := strconv.ParseInt(s, 10, 64); err == nil {
						return jsonNumber(strconv.FormatInt(n, 10))
					}
					if _, err := strconv.ParseFloat(s, 64); err == nil {
						return jsonNumber(s)
					}
					return s
				}
				return inner
			case "BOOL":
				return inner
			case "NULL":
				return nil
			case "L":
				if arr := asArray(inner); arr != nil {
					out := make([]any, len(arr))
					for i, v := range arr {
						out[i] = unwrapDynamoDBValue(v, depth+1)
					}
					return out
				}
			case "M":
				if m := asObject(inner); m != nil {
					out := newJSONObject()
					for _, k := range m.keys() {
						out.set(k, unwrapDynamoDBValue(m.get(k), depth+1))
					}
					return out
				}
			case "SS":
				return inner
			case "NS":
				if arr := asArray(inner); arr != nil {
					var nums []any
					for _, v := range arr {
						s, ok := asString(v)
						if !ok {
							continue
						}
						if n, err := strconv.ParseInt(s, 10, 64); err == nil {
							nums = append(nums, jsonNumber(strconv.FormatInt(n, 10)))
						} else if _, err := strconv.ParseFloat(s, 64); err == nil {
							nums = append(nums, jsonNumber(s))
						} else {
							nums = append(nums, s)
						}
					}
					if nums == nil {
						nums = []any{}
					}
					return nums
				}
				return inner
			case "BS":
				return inner
			}
		}
		// Not a DynamoDB type wrapper — unwrap each field.
		out := newJSONObject()
		for _, k := range obj.keys() {
			out.set(k, unwrapDynamoDBValue(obj.get(k), depth+1))
		}
		return out
	}

	return val
}

func filterDynamoDBItems(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	items := asArray(get(v, "Items"))
	if items == nil {
		return filterResult{}, false
	}

	count := i64Or(get(v, "Count"), int64(len(items)))
	scanned := i64Or(get(v, "ScannedCount"), count)
	total := len(items)
	truncated := total > maxItems

	var lines []string
	lines = append(lines, fmt.Sprintf("Count: %d/%d", count, scanned))

	if capacity := asObject(get(v, "ConsumedCapacity")); capacity != nil {
		if units, ok := asF64(capacity.get("CapacityUnits")); ok {
			lines = append(lines, fmt.Sprintf("Capacity: %s RCU", formatF64(units)))
		}
	}

	if isObject(get(v, "LastEvaluatedKey")) {
		lines = append(lines, "(paginated — more results available)")
	}

	for i, item := range items {
		if i >= maxItems {
			break
		}
		unwrapped := unwrapDynamoDBValue(item, 0)
		compact, ok := marshalCompact(unwrapped)
		if !ok {
			compact = "?"
		}
		lines = append(lines, compact)
	}

	if truncated {
		lines = append(lines, fmt.Sprintf("… +%d more items", total-maxItems))
	}

	text := strings.Join(lines, "\n")
	return resultWithOverflow(text, truncated), true
}

func filterECSTasks(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	tasks := asArray(get(v, "tasks"))
	if tasks == nil {
		return filterResult{}, false
	}

	total := len(tasks)
	truncated := total > maxItems
	var result []string

	for i, task := range tasks {
		if i >= maxItems {
			break
		}
		taskArn := strOr(get(task, "taskArn"), "?")
		taskID := shortenARN(taskArn)
		status := strOr(get(task, "lastStatus"), "?")

		var containers []string
		for _, c := range asArray(get(task, "containers")) {
			cname := strOr(get(c, "name"), "?")
			cstatus := strOr(get(c, "lastStatus"), "?")
			if code, ok := asI64(get(c, "exitCode")); ok {
				containers = append(containers, fmt.Sprintf("%s:%s(exit:%d)", cname, cstatus, code))
			} else {
				containers = append(containers, fmt.Sprintf("%s:%s", cname, cstatus))
			}
		}

		stoppedReason := strOr(get(task, "stoppedReason"), "")
		reasonStr := ""
		if stoppedReason != "" {
			reasonStr = fmt.Sprintf(" reason:%s", stoppedReason)
		}

		result = append(result, fmt.Sprintf(
			"%s %s containers:[%s]%s",
			taskID, status, strings.Join(containers, ", "), reasonStr,
		))
	}

	text := joinWithOverflow(result, total, maxItems, "tasks")
	return resultWithOverflow(text, truncated), true
}

// --- P2 filters: Security Groups, S3 objects, EKS, SQS ---

func formatSGRule(perm any) string {
	protocol := strOr(get(perm, "IpProtocol"), "?")
	proto := protocol
	if protocol == "-1" {
		proto = "all"
	}

	fromPort, hasFrom := asI64(get(perm, "FromPort"))
	toPort, hasTo := asI64(get(perm, "ToPort"))
	port := "*"
	if hasFrom && hasTo {
		if fromPort == toPort {
			port = fmt.Sprintf("%d", fromPort)
		} else {
			port = fmt.Sprintf("%d-%d", fromPort, toPort)
		}
	}

	var sources []string
	for _, r := range asArray(get(perm, "IpRanges")) {
		if cidr, ok := asString(get(r, "CidrIp")); ok {
			sources = append(sources, cidr)
		}
	}
	for _, r := range asArray(get(perm, "Ipv6Ranges")) {
		if cidr, ok := asString(get(r, "CidrIpv6")); ok {
			sources = append(sources, cidr)
		}
	}
	for _, g := range asArray(get(perm, "UserIdGroupPairs")) {
		sources = append(sources, strOr(get(g, "GroupId"), "?"))
	}

	src := "?"
	if len(sources) > 0 {
		src = strings.Join(sources, ",")
	}

	if proto == "all" {
		return fmt.Sprintf("all<-%s", src)
	}
	return fmt.Sprintf("%s/%s<-%s", proto, port, src)
}

func filterSecurityGroups(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	groups := asArray(get(v, "SecurityGroups"))
	if groups == nil {
		return filterResult{}, false
	}

	total := len(groups)
	truncated := total > maxItems
	var result []string

	for i, sg := range groups {
		if i >= maxItems {
			break
		}
		name := strOr(get(sg, "GroupName"), "?")
		id := strOr(get(sg, "GroupId"), "?")

		var ingress, egress []string
		for _, p := range asArray(get(sg, "IpPermissions")) {
			ingress = append(ingress, formatSGRule(p))
		}
		for _, p := range asArray(get(sg, "IpPermissionsEgress")) {
			egress = append(egress, formatSGRule(p))
		}

		ingressStr := "none"
		if len(ingress) > 0 {
			ingressStr = strings.Join(ingress, ", ")
		}
		egressStr := "none"
		if len(egress) > 0 {
			egressStr = strings.Join(egress, ", ")
		}

		result = append(result, fmt.Sprintf("%s (%s) ingress: %s | egress: %s", name, id, ingressStr, egressStr))
	}

	text := joinWithOverflow(result, total, maxItems, "groups")
	return resultWithOverflow(text, truncated), true
}

func filterS3Objects(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	contents := asArray(get(v, "Contents")) // may be nil (treated as empty)

	total := len(contents)
	truncated := total > maxItems
	var result []string

	for i, obj := range contents {
		if i >= maxItems {
			break
		}
		key := strOr(get(obj, "Key"), "?")
		size := uint64(0)
		if n, ok := asI64(get(obj, "Size")); ok && n >= 0 {
			size = uint64(n)
		}
		modified := "?"
		if s, ok := asString(get(obj, "LastModified")); ok {
			modified = truncateISODate(s)
		}
		result = append(result, fmt.Sprintf("%s %s %s", key, humanBytes(size), modified))
	}

	text := joinWithOverflow(result, total, maxItems, "objects")
	return resultWithOverflow(text, truncated), true
}

func filterEKSCluster(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	cluster := get(v, "cluster")

	name := strOr(get(cluster, "name"), "?")
	status := strOr(get(cluster, "status"), "?")
	version := strOr(get(cluster, "version"), "?")
	endpoint := strOr(get(cluster, "endpoint"), "?")
	// certificateAuthority intentionally NOT read (base64 cert, 1000+ chars).

	text := fmt.Sprintf("%s %s k8s/%s %s", name, status, version, endpoint)
	return newResult(text), true
}

var s3TransferRE = regexp.MustCompile(`^(upload|download|delete|copy|move):`)

func filterSQSMessages(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}
	messages := asArray(get(v, "Messages")) // may be nil (treated as empty)

	total := len(messages)
	truncated := total > maxItems
	var result []string

	for i, msg := range messages {
		if i >= maxItems {
			break
		}
		id := strOr(get(msg, "MessageId"), "?")
		idShort := id
		if len(idShort) > 8 {
			idShort = idShort[:8]
		}
		body := strOr(get(msg, "Body"), "?")
		bodyTruncated := truncateText(body, 200)
		// ReceiptHandle intentionally NOT read (200+ chars of opaque garbage).
		result = append(result, fmt.Sprintf("%s %s", idShort, bodyTruncated))
	}

	text := joinWithOverflow(result, total, maxItems, "messages")
	return resultWithOverflow(text, truncated), true
}

func filterDynamoDBGetItem(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}

	var lines []string

	if item := asObject(get(v, "Item")); item != nil {
		unwrapped := unwrapDynamoDBValue(item, 0)
		compact, ok := marshalCompact(unwrapped)
		if !ok {
			compact = "?"
		}
		lines = append(lines, compact)
	}

	if capacity := asObject(get(v, "ConsumedCapacity")); capacity != nil {
		if units, ok := asF64(capacity.get("CapacityUnits")); ok {
			lines = append(lines, fmt.Sprintf("Capacity: %s RCU", formatF64(units)))
		}
	}

	if len(lines) == 0 {
		return filterResult{}, false
	}

	return newResult(strings.Join(lines, "\n")), true
}

func filterLogsQueryResults(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}

	var lines []string

	if status, ok := asString(get(v, "status")); ok {
		lines = append(lines, fmt.Sprintf("Status: %s", status))
	}

	results := asArray(get(v, "results"))
	if results == nil {
		return filterResult{}, false
	}

	total := len(results)
	truncated := total > maxItems

	for i, row := range results {
		if i >= maxItems {
			break
		}
		fields := asArray(row)
		if fields == nil {
			continue
		}
		var fieldPairs []string
		for _, field := range fields {
			fieldName, ok := asString(get(field, "field"))
			if !ok {
				continue
			}
			if fieldName == "@ptr" {
				continue
			}
			fieldValue := get(field, "value")
			var valStr string
			if s, ok := asString(fieldValue); ok {
				valStr = s
			} else {
				// numbers, booleans, null
				if c, ok := marshalCompact(fieldValue); ok {
					valStr = c
				}
			}
			fieldPairs = append(fieldPairs, fmt.Sprintf("%s=%s", fieldName, valStr))
		}
		lines = append(lines, strings.Join(fieldPairs, " "))
	}

	if truncated {
		lines = append(lines, fmt.Sprintf("… +%d more rows", total-maxItems))
	}

	text := strings.Join(lines, "\n")
	return resultWithOverflow(text, truncated), true
}

func filterS3Transfer(output string) filterResult {
	lines := splitLines(output)
	total := len(lines)

	// Pass through short output unchanged.
	if total < 10 {
		return newResult(output)
	}

	uploaded, downloaded, deleted, copied, moved := 0, 0, 0, 0, 0
	var errs []string

	for _, line := range lines {
		if m := s3TransferRE.FindStringSubmatch(line); m != nil {
			switch m[1] {
			case "upload":
				uploaded++
			case "download":
				downloaded++
			case "delete":
				deleted++
			case "copy":
				copied++
			case "move":
				moved++
			}
		} else if strings.Contains(line, "error") || strings.Contains(line, "failed") {
			errs = append(errs, line)
		}
	}

	var summaryParts []string
	if uploaded > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d uploaded", uploaded))
	}
	if downloaded > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d downloaded", downloaded))
	}
	if deleted > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d deleted", deleted))
	}
	if copied > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d copied", copied))
	}
	if moved > 0 {
		summaryParts = append(summaryParts, fmt.Sprintf("%d moved", moved))
	}

	var resultLines []string
	if len(summaryParts) > 0 {
		resultLines = append(resultLines, fmt.Sprintf(
			"S3 transfer: %s, %d errors", strings.Join(summaryParts, ", "), len(errs),
		))
	}

	for i, e := range errs {
		if i >= 10 {
			break
		}
		resultLines = append(resultLines, e)
	}

	if len(resultLines) == 0 {
		return newResult(output)
	}

	return newResult(strings.Join(resultLines, "\n"))
}

func filterSecretsGet(jsonStr string) (filterResult, bool) {
	v, ok := parseJSON(jsonStr)
	if !ok {
		return filterResult{}, false
	}

	var lines []string

	if name, ok := asString(get(v, "Name")); ok {
		lines = append(lines, fmt.Sprintf("Name: %s", name))
	}

	if secretStr, ok := asString(get(v, "SecretString")); ok {
		// Try to parse as JSON and compact it.
		if sv, ok := parseJSON(secretStr); ok {
			if compact, ok := marshalCompact(sv); ok {
				lines = append(lines, fmt.Sprintf("Secret: %s", compact))
			} else {
				lines = append(lines, fmt.Sprintf("Secret: %s", secretStr))
			}
		} else {
			lines = append(lines, fmt.Sprintf("Secret: %s", secretStr))
		}
	}

	if len(lines) == 0 {
		return filterResult{}, false
	}

	return newResult(strings.Join(lines, "\n")), true
}

// --- small shared helpers ---

// resultWithOverflow builds a filterResult flagged truncated when overflow is
// true.
func resultWithOverflow(text string, truncated bool) filterResult {
	if truncated {
		return truncatedResult(text)
	}
	return newResult(text)
}

// splitLines splits text into lines, dropping a single trailing empty element
// so it matches Rust's str::lines() semantics.
func splitLines(text string) []string {
	if text == "" {
		return nil
	}
	parts := strings.Split(text, "\n")
	if len(parts) > 0 && parts[len(parts)-1] == "" {
		parts = parts[:len(parts)-1]
	}
	return parts
}

// rsplitN splits s on sep, from the right, into at most n pieces, returning
// them in right-to-left order to mirror Rust's str::rsplitn collected into a
// Vec.
func rsplitN(s string, sep byte, n int) []string {
	var parts []string
	for n > 1 {
		idx := strings.LastIndexByte(s, sep)
		if idx < 0 {
			break
		}
		parts = append(parts, s[idx+1:])
		s = s[:idx]
		n--
	}
	parts = append(parts, s)
	return parts
}

// dedupConsecutive removes consecutive duplicate strings, mirroring Rust's
// Vec::dedup (which only collapses adjacent equal elements).
func dedupConsecutive(in []string) []string {
	if len(in) == 0 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

// formatF64 renders a float the way serde_json/Rust's {} does for these RCU
// values: whole numbers without a trailing ".0" (e.g. 1.0 -> "1", 2.5 -> "2.5").
func formatF64(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
