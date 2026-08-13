#!/usr/bin/env bash
set -euo pipefail

repo=$(cd "$(dirname "$0")/.." && pwd)
workflow="$repo/.github/workflows/release.yml"

WORKFLOW_PATH="$workflow" ruby <<'RUBY'
require "yaml"

workflow_path = ENV.fetch("WORKFLOW_PATH")
workflow = YAML.load_file(workflow_path)
trigger = workflow["on"] || workflow[true]
abort "workflow has no trigger definition" unless trigger.is_a?(Hash)

dispatch = trigger.fetch("workflow_dispatch")
input = dispatch.fetch("inputs").fetch("skip_signing")
abort "skip_signing must be boolean" unless input["type"] == "boolean"
abort "skip_signing must default to true" unless input["default"] == true

steps = workflow.fetch("jobs").fetch("release").fetch("steps")
gate = steps.find { |step| step["name"] == "Signing gate" }
abort "Signing gate step is missing" unless gate
gate_env = gate.fetch("env")
abort "Signing gate must receive the Sparkle private key" unless gate_env.key?("SPARKLE_KEY")
abort "Signing gate must require the Sparkle private key" unless gate.fetch("run").include?("$SPARKLE_KEY")
RUBY

echo "ALL PASS"
