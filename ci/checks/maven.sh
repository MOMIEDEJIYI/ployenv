#!/usr/bin/env bash
# Maven checks that cannot be expressed as one table row: the row only says
# "mvn -v matches ^Apache Maven 3\.". These are the semantics that must not rot
# (REQ-009): a stable 3.x, the JDK handed over by "ployenv env", no Maven state,
# and a release candidate that stays opt-in.
set -euo pipefail

BIN="${PLOYENV_BIN:?the job must export PLOYENV_BIN}"
WORK="${RUNNER_TEMP:-/tmp}"

test -z "${MAVEN_HOME:-}" || { echo "::error::env must not set MAVEN_HOME (got $MAVEN_HOME)"; exit 1; }
rm -rf "$HOME/.m2"

mvn -v | tee "$WORK/mvn-v.txt"
grep -Eq '^Apache Maven 3\.' "$WORK/mvn-v.txt" || { echo '::error::maven is not a stable 3.x release'; exit 1; }
if grep -Eq '^Apache Maven 4\.' "$WORK/mvn-v.txt"; then echo '::error::a Maven release candidate was installed'; exit 1; fi
JAVA_HOME="${JAVA_HOME:?ployenv env must export JAVA_HOME for maven}"
grep -Fq "runtime: $JAVA_HOME" "$WORK/mvn-v.txt" || { echo "::error::mvn does not run on the JDK that ployenv activated ($JAVA_HOME)"; exit 1; }
test ! -e "$HOME/.m2" || { echo "::error::mvn wrote ~/.m2"; exit 1; }
test -x "$PLOYENV_ROOT"/versions/maven/*/bin/mvn || { echo "::error::the mvn wrapper is not executable"; exit 1; }

if "$BIN" install maven@release > "$WORK/mvn-release.txt" 2>&1; then
  echo '::error::@release must be refused while upstream <release> is 4.0.0-rc-7'
  exit 1
fi
grep -Fq '4.0.0-rc-7' "$WORK/mvn-release.txt" || { echo '::error::the refusal does not name the release candidate'; exit 1; }
if "$BIN" available maven | grep -q '4\.0\.0-rc'; then echo "::error::the default listing shows a release candidate"; exit 1; fi

echo 'PASS maven: stable 3.x on the managed JDK, no ~/.m2, @release refused'
