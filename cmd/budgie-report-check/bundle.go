package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/juncoflockleader/budgie-bbs/internal/runbundle"
	"github.com/juncoflockleader/budgie-bbs/internal/runconfig"
)

func runBundle(args []string, stdout, stderr io.Writer) int {
	flags := newReportCheckFlagSet("bundle", stderr)
	preflightReport := flags.String("preflight-report", "", "Optional internet-scale preflight JSON report")
	gatewayReport := flags.String("gateway-report", "", "Optional gateway fanout JSON report")
	natsReport := flags.String("nats-report", "", "Optional native NATS command-log JSON report")
	kafkaReport := flags.String("kafka-report", "", "Optional native Kafka command-log JSON report")
	manifestFile := flags.String("manifest-file", "", "Optional JSON manifest path written after a successful bundle check")
	verifyManifest := flags.String("verify-manifest", "", "Optional JSON manifest path to verify against referenced report files")
	targets := flags.String("targets", "", "Comma-separated bundle targets to record in the manifest: gateway,nats,kafka,all")
	remoteStaging := flags.Bool("remote-staging", false, "Record this bundle as remote/shared staging evidence")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintf(stderr, "unsupported argument %q; use flags only\n", flags.Arg(0))
		return 2
	}

	targetRaw := strings.TrimSpace(*targets)
	verifyManifestPath := strings.TrimSpace(*verifyManifest)
	manifestPath := strings.TrimSpace(*manifestFile)
	targetList, err := runconfig.NormalizeOrderedTargets(targetRaw, nil, runbundle.ManifestTargetOrder(), "bundle manifest")
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if verifyManifestPath != "" {
		violations, err := runbundle.VerifyManifest(verifyManifestPath, runbundle.ManifestVerifyOptions{
			ExpectedTargets:       targetList,
			RequireTargets:        targetRaw != "",
			ExpectedRemoteStaging: *remoteStaging,
			RequireRemoteStaging:  *remoteStaging,
		})
		if err != nil {
			fmt.Fprintf(stderr, "verify bundle manifest: %v\n", err)
			return 2
		}
		if len(violations) > 0 {
			fmt.Fprintf(stderr, "internet-scale bundle manifest violations: %s\n", strings.Join(violations, "; "))
			return 3
		}
		fmt.Fprintf(stdout, "internet-scale bundle manifest verified at %s\n", verifyManifestPath)
		return 0
	}

	specs := runbundle.SelectedReportSpecs(*preflightReport, *gatewayReport, *natsReport, *kafkaReport)
	if len(specs) == 0 {
		fmt.Fprintln(stderr, "at least one report path is required")
		return 2
	}
	violations := runbundle.EvaluateReports(specs)
	if len(violations) > 0 {
		fmt.Fprintf(stderr, "internet-scale bundle evidence violations: %s\n", strings.Join(violations, "; "))
		return 3
	}
	if manifestPath != "" {
		if err := runbundle.WriteManifest(manifestPath, specs, runbundle.ManifestOptions{Targets: targetList, RemoteStaging: *remoteStaging}); err != nil {
			fmt.Fprintf(stderr, "write bundle manifest: %v\n", err)
			return 2
		}
		fmt.Fprintf(stdout, "archived internet-scale bundle manifest at %s\n", manifestPath)
	}
	fmt.Fprintln(stdout, "internet-scale report bundle has consistent evidence metadata")
	return 0
}
