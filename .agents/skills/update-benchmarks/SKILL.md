---
name: update-benchmarks
description: >
  Update the website benchmark data by running the bench-compare harness and transforming results for the website.
  Use when the user asks to refresh benchmarks, update benchmark data, rerun performance comparisons,
  or regenerate the benchmarks page. Also use when the user mentions benchmark numbers are stale or out of date.
---

# Update Benchmarks

Refreshes the benchmark data displayed on the website's Performance > Benchmarks page.

## Steps

### 1. Run the benchmark harness

```bash
make bench-compare BENCH_COMPARE_RUNS=50 JSON_OUT=/tmp/bench-compare.json
```

The default is 100 runs but 50 is sufficient for stable medians. The user may request a different count.

### 2. Collect machine info

Gather the current machine's specs for the test environment table:

```bash
# Model identifier
sysctl -n hw.model                    # e.g. Mac15,8
# Chip name
sysctl -n machdep.cpu.brand_string    # e.g. Apple M3 Max
# Core counts
sysctl -n hw.perflevel0.physicalcpu   # performance cores
sysctl -n hw.perflevel1.physicalcpu   # efficiency cores
# Memory
sysctl -n hw.memsize                  # bytes, divide by 1073741824 for GB
# OS version
sw_vers -productName && sw_vers -productVersion  # e.g. macOS 15.5
# Go version
go version                            # e.g. go1.26.1 darwin/arm64
```

### 3. Transform JSON for the website

Use Python to strip individual trial data (keeping only stats) and inject the machine info:

```python
import json

with open("/tmp/bench-compare.json") as f:
    data = json.load(f)

for scenario in data["scenarios"]:
    for result in scenario["results"]:
        del result["trials"]

data["machine"] = {
    "model": "<from step 2>",
    "chip": "<from step 2>",
    "cores": "<N> (<P> performance + <E> efficiency)",
    "memory": "<N> GB",
    "os": "<name> <version>",
    "go_version": "<go version output>"
}

with open("website/content/performance/benchmark-data.json", "w") as f:
    json.dump(data, f, indent=2)
```

### 4. Verify the build

```bash
cd website && npm run build
```

Confirm `/docs/performance/benchmarks` appears in the route list.

## Key files

- `scripts/bench-compare/main.go` — the benchmark harness
- `website/content/performance/benchmark-data.json` — transformed JSON consumed by the website
- `website/app/components/docs/BenchmarkChart.tsx` — React component rendering the data
- `website/content/performance/benchmarks.mdx` — the benchmarks page content
- `Makefile` — `bench-compare` target and its variables (`BENCH_COMPARE_RUNS`, `JUST_BASH_SPEC`, `JSON_OUT`)
