// Package aws is gortk's token-optimized AWS CLI wrapper. It forces JSON output
// for structured read operations and compresses the verbose JSON into compact,
// LLM-friendly summaries. Faithful port of rtk's src/cmds/cloud/aws_cmd.rs.
//
// Like rtk, this wraps the platform `aws` CLI; gortk resolves it PATHEXT-aware.
// It is offline by default: the only process it spawns is `aws` itself. No
// telemetry, no network calls of its own.
package aws

import (
	"fmt"
	"os"
	"strings"

	"gortk/internal/core"
	"gortk/internal/registry"
)

func init() {
	registry.Register(&registry.Cmd{
		Name:    "aws",
		Summary: "AWS CLI with compact output (force JSON, compress)",
		Run:     Run,
	})
}

// maxItems caps flat lists (instances, services, stacks...). Mirrors rtk's
// MAX_ITEMS = CAP_LIST.
const maxItems = core.CapList

// maxLogEvents caps CloudWatch log events. Mirrors rtk's MAX_LOG_EVENTS =
// CAP_INVENTORY.
const maxLogEvents = core.CapInventory

// jsonCompressDepth is the recursion cap for the generic JSON compactor.
const jsonCompressDepth = 4

// filterResult is the outcome of a filter function: the filtered text plus
// whether items were truncated. When truncated is true, the runner force-tees
// the full raw output so the LLM has a recovery path. Mirrors rtk's
// FilterResult.
type filterResult struct {
	text      string
	truncated bool
}

func newResult(text string) filterResult        { return filterResult{text: text} }
func truncatedResult(text string) filterResult   { return filterResult{text: text, truncated: true} }

// filterFn matches rtk's `fn(&str) -> Option<FilterResult>`: it returns
// (result, ok) where ok=false means "fall back to raw output".
type filterFn func(jsonStr string) (filterResult, bool)

// Run executes the aws command. args are everything after the "aws" command
// name; the first element is the AWS service subcommand (e.g. "sts", "s3").
func Run(args []string, verbose int) (int, error) {
	if len(args) == 0 {
		return runGeneric("", nil, verbose)
	}

	subcommand := args[0]
	rest := args[1:]

	// firstRest is the first argument after the subcommand, used for routing
	// (e.g. "sts" + "get-caller-identity").
	firstRest := ""
	if len(rest) > 0 {
		firstRest = rest[0]
	}

	switch subcommand {
	case "sts":
		if firstRest == "get-caller-identity" {
			return runAWSFiltered([]string{"sts", "get-caller-identity"}, rest[1:], verbose, filterSTSIdentity)
		}
	case "s3":
		switch firstRest {
		case "ls":
			return runS3Ls(rest[1:], verbose)
		case "sync", "cp":
			return runS3Transfer(firstRest, rest[1:], verbose)
		}
	case "ec2":
		switch firstRest {
		case "describe-instances":
			return runAWSFiltered([]string{"ec2", "describe-instances"}, rest[1:], verbose, filterEC2Instances)
		case "describe-security-groups":
			return runAWSFiltered([]string{"ec2", "describe-security-groups"}, rest[1:], verbose, filterSecurityGroups)
		}
	case "ecs":
		switch firstRest {
		case "list-services":
			return runAWSFiltered([]string{"ecs", "list-services"}, rest[1:], verbose, filterECSListServices)
		case "describe-services":
			return runAWSFiltered([]string{"ecs", "describe-services"}, rest[1:], verbose, filterECSDescribeServices)
		case "describe-tasks":
			return runAWSFiltered([]string{"ecs", "describe-tasks"}, rest[1:], verbose, filterECSTasks)
		}
	case "rds":
		if firstRest == "describe-db-instances" {
			return runAWSFiltered([]string{"rds", "describe-db-instances"}, rest[1:], verbose, filterRDSInstances)
		}
	case "cloudformation":
		switch firstRest {
		case "list-stacks":
			return runAWSFiltered([]string{"cloudformation", "list-stacks"}, rest[1:], verbose, filterCFNListStacks)
		case "describe-stacks":
			return runAWSFiltered([]string{"cloudformation", "describe-stacks"}, rest[1:], verbose, filterCFNDescribeStacks)
		case "describe-stack-events":
			return runAWSFiltered([]string{"cloudformation", "describe-stack-events"}, rest[1:], verbose, filterCFNEvents)
		}
	case "logs":
		switch firstRest {
		case "get-log-events", "filter-log-events":
			return runAWSFiltered([]string{"logs", firstRest}, rest[1:], verbose, filterLogsEvents)
		case "get-query-results":
			return runAWSFiltered([]string{"logs", "get-query-results"}, rest[1:], verbose, filterLogsQueryResults)
		}
	case "lambda":
		switch firstRest {
		case "list-functions":
			return runAWSFiltered([]string{"lambda", "list-functions"}, rest[1:], verbose, filterLambdaList)
		case "get-function":
			return runAWSFiltered([]string{"lambda", "get-function"}, rest[1:], verbose, filterLambdaGet)
		}
	case "iam":
		switch firstRest {
		case "list-roles":
			return runAWSFiltered([]string{"iam", "list-roles"}, rest[1:], verbose, filterIAMRoles)
		case "list-users":
			return runAWSFiltered([]string{"iam", "list-users"}, rest[1:], verbose, filterIAMUsers)
		}
	case "dynamodb":
		switch firstRest {
		case "scan", "query":
			return runAWSFiltered([]string{"dynamodb", firstRest}, rest[1:], verbose, filterDynamoDBItems)
		case "get-item":
			return runAWSFiltered([]string{"dynamodb", "get-item"}, rest[1:], verbose, filterDynamoDBGetItem)
		}
	case "s3api":
		if firstRest == "list-objects-v2" {
			return runAWSFiltered([]string{"s3api", "list-objects-v2"}, rest[1:], verbose, filterS3Objects)
		}
	case "eks":
		if firstRest == "describe-cluster" {
			return runAWSFiltered([]string{"eks", "describe-cluster"}, rest[1:], verbose, filterEKSCluster)
		}
	case "sqs":
		if firstRest == "receive-message" {
			return runAWSFiltered([]string{"sqs", "receive-message"}, rest[1:], verbose, filterSQSMessages)
		}
	case "secretsmanager":
		if firstRest == "get-secret-value" {
			return runAWSFiltered([]string{"secretsmanager", "get-secret-value"}, rest[1:], verbose, filterSecretsGet)
		}
	}

	return runGeneric(subcommand, rest, verbose)
}

// isStructuredOperation reports whether the operation returns structured JSON
// (describe-*, list-*, get-*, scan, query, receive-message). Mutating/transfer
// operations (s3 cp, s3 sync) emit plain-text progress and reject --output json.
func isStructuredOperation(args []string) bool {
	op := ""
	if len(args) > 0 {
		op = args[0]
	}
	if op == "sync" || op == "cp" {
		return false
	}
	return strings.HasPrefix(op, "describe-") ||
		strings.HasPrefix(op, "list-") ||
		strings.HasPrefix(op, "get-") ||
		op == "scan" ||
		op == "query" ||
		op == "receive-message"
}

// runGeneric is the fallback strategy: force --output json for structured ops,
// then compress via the generic JSON compactor (values preserved).
func runGeneric(subcommand string, args []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("aws")
	if subcommand != "" {
		cmd.Args = append(cmd.Args, subcommand)
	}

	hasOutputFlag := false
	for _, arg := range args {
		if arg == "--output" {
			hasOutputFlag = true
		}
		cmd.Args = append(cmd.Args, arg)
	}

	if !hasOutputFlag && isStructuredOperation(args) {
		cmd.Args = append(cmd.Args, "--output", "json")
	}

	fullSub := subcommand
	if len(args) > 0 {
		fullSub = strings.TrimSpace(subcommand + " " + strings.Join(args, " "))
	}

	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true}
	return core.RunFiltered(cmd, "aws", fullSub, func(raw string) string {
		if verbose > 0 {
			fmt.Fprintf(os.Stderr, "Running: aws %s\n", fullSub)
		}
		compact, ok := filterJSONCompact(raw, jsonCompressDepth)
		if !ok {
			// Fallback: print raw (maybe not JSON).
			return raw
		}
		return compact
	}, opts)
}

// runAWSFiltered is the shared runner for AWS commands that return JSON. It
// builds the argv (forcing --output json, replacing any existing --output),
// runs aws, and applies filterFn to compress the stdout JSON.
func runAWSFiltered(subArgs []string, extraArgs []string, verbose int, filter filterFn) (int, error) {
	cmd := core.ResolvedCommand("aws")
	cmd.Args = append(cmd.Args, subArgs...)

	// Replace --output table/text with --output json.
	skipNext := false
	for _, arg := range extraArgs {
		if skipNext {
			skipNext = false
			continue
		}
		if arg == "--output" {
			skipNext = true
			continue
		}
		if strings.HasPrefix(arg, "--output=") {
			continue
		}
		cmd.Args = append(cmd.Args, arg)
	}
	cmd.Args = append(cmd.Args, "--output", "json")

	cmdLabel := strings.Join(subArgs, " ")
	teeLabel := "aws_" + strings.ReplaceAll(cmdLabel, " ", "_")

	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true, TeeLabel: teeLabel}
	return core.RunFiltered(cmd, "aws", cmdLabel, func(stdout string) string {
		if verbose > 0 {
			fmt.Fprintf(os.Stderr, "Running: aws %s\n", cmdLabel)
		}
		res, ok := filter(stdout)
		if !ok {
			fmt.Fprintln(os.Stderr, "gortk: filter warning: aws filter returned None, passing through raw output")
			return stdout
		}
		return res.text
	}, opts)
}

// runS3Ls wraps `aws s3 ls` (text output, not JSON).
func runS3Ls(extraArgs []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("aws", "s3", "ls")
	cmd.Args = append(cmd.Args, extraArgs...)

	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true, TeeLabel: "aws_s3_ls"}
	return core.RunFiltered(cmd, "aws", "s3 ls", func(stdout string) string {
		if verbose > 0 {
			fmt.Fprintf(os.Stderr, "Running: aws s3 ls %s\n", strings.Join(extraArgs, " "))
		}
		return filterS3Ls(stdout).text
	}, opts)
}

// runS3Transfer wraps `aws s3 sync` / `aws s3 cp` (text output, not JSON).
func runS3Transfer(operation string, extraArgs []string, verbose int) (int, error) {
	cmd := core.ResolvedCommand("aws", "s3", operation)
	cmd.Args = append(cmd.Args, extraArgs...)

	cmdLabel := "s3 " + operation
	teeLabel := "aws_s3_" + operation

	opts := core.RunOptions{FilterStdoutOnly: true, SkipFilterOnFailure: true, TeeLabel: teeLabel}
	return core.RunFiltered(cmd, "aws", cmdLabel, func(stdout string) string {
		if verbose > 0 {
			fmt.Fprintf(os.Stderr, "Running: aws %s %s\n", cmdLabel, strings.Join(extraArgs, " "))
		}
		return filterS3Transfer(stdout).text
	}, opts)
}
