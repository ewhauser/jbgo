#!/usr/bin/env bash

set -euo pipefail

# Re-run each new fuzz reproducer and open a PR for any that fail with
# errors other than "deadline exceeded".

job_name="${1:?usage: open_fuzz_pr.sh <job-name>}"

# Collect new/modified corpus files.
files=()
while IFS= read -r path; do
  [ -n "$path" ] || continue
  rel="${path#./}"
  if git ls-files --error-unmatch -- "$rel" >/dev/null 2>&1; then
    if [ -z "$(git status --short -- "$rel")" ]; then
      continue
    fi
  fi
  files+=("$path")
done < <(find . -type f -path '*/testdata/fuzz/Fuzz*/*' | LC_ALL=C sort)

if [ "${#files[@]}" -eq 0 ]; then
  echo "No new corpus files found."
  exit 0
fi

# Re-run each reproducer and keep only those that are NOT deadline exceeded.
real_files=()
for path in "${files[@]}"; do
  rel="${path#./}"
  pkg="${rel%%/testdata/fuzz/*}"
  suffix="${rel#"${pkg}"/testdata/fuzz/}"
  fuzz_target="${suffix%%/*}"
  corpus="${suffix#"${fuzz_target}"/}"

  echo "==> Verifying $rel"
  output=$(go test "./$pkg" -run="^${fuzz_target}/${corpus}\$" -count=1 -timeout=60s 2>&1 || true)

  if echo "$output" | grep -qi "deadline exceeded"; then
    echo "    Skipped (deadline exceeded)"
    git checkout -- "$path" 2>/dev/null || rm -f "$path"
  else
    echo "    Real failure — keeping"
    real_files+=("$rel")
  fi
done

if [ "${#real_files[@]}" -eq 0 ]; then
  echo "All reproducers were deadline exceeded. No PR needed."
  exit 0
fi

branch="fuzz/reproducers-$(date -u +%Y%m%d-%H%M%S)"
git checkout -b "$branch"
git add "${real_files[@]}"
git commit -m "fuzz: add $([ ${#real_files[@]} -eq 1 ] && echo "reproducer" || echo "${#real_files[@]} reproducers") from ${job_name}"

git push origin "$branch"

body="The nightly \`${job_name}\` fuzzing run found"
if [ "${#real_files[@]}" -eq 1 ]; then
  body="$body a new reproducer."
else
  body="$body ${#real_files[@]} new reproducers."
fi
body="$body"$'\n\n'"**Files:**"
for f in "${real_files[@]}"; do
  body="$body"$'\n'"- \`$f\`"
done
body="$body"$'\n\n'"Each file can be re-run locally with \`go test ./<pkg> -run=<FuzzTarget>/<corpus>\`."

gh pr create \
  --title "fuzz: add reproducers from ${job_name}" \
  --body "$body" \
  --base main \
  --head "$branch"
