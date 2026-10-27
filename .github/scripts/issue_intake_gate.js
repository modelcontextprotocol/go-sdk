// Issue intake gate. The policy lives in CONTRIBUTING.md ("How issues get
// in"); .github/workflows/require-issue-checklist.yml wires this up to events.
//
// An issue from someone without triage rights stays open only if its
// description ticks every item in REQUIRED_CHECKS: the attestations that the
// issue forms in .github/ISSUE_TEMPLATE make required. The forms enforce them
// in the web UI, but issues filed through the API or `gh issue create` skip
// the forms, and a ticked box can be unticked by a later edit. Otherwise the
// gate labels the issue `missing-checklist`, leaves one comment listing what's
// missing, and closes it as not planned. It re-evaluates — and reopens — the
// issue when the description is edited. A triage+ user reopening the issue,
// removing the label, or adding `bypass-checklist` overrides it, and the
// override sticks.
//
// Everything that writes goes through mutate(); when the workflow passes
// ENFORCE=false (its kill switch) the run only logs what it would have done.
'use strict';

const LABEL = 'missing-checklist'; // marks issues the gate has closed
const BYPASS_LABEL = 'bypass-checklist'; // sticky maintainer override
const MARKER = '<!-- require-issue-checklist -->';
const BOT_LOGIN = 'github-actions[bot]';

// Each issue form carries these as its required checkboxes, word for word, and
// has no other required ones (issue_intake_gate.test.js checks that). Forms
// may add optional checkboxes, such as whether the problem was hit in
// production; the gate ignores those.
const REQUIRED_CHECKS = [
  'I searched the existing issues, and this has not been reported before.',
  'This is a real problem that I ran into myself, using the latest release of the SDK.',
  'I (a human) examined the relevant SDK code, and wrote or reviewed every line of this report. It is not unreviewed AI output.',
  'This report is brief and concise, and sticks to the facts needed to understand the problem.',
];

module.exports = async function run({ github, context, core }) {
  const { owner, repo } = context.repo;
  const enforce = process.env.ENFORCE === 'true';
  const contributingUrl = `https://github.com/${owner}/${repo}/blob/main/CONTRIBUTING.md#how-issues-get-in`;

  // ── Entry points ─────────────────────────────────────────────────────────

  if (context.eventName === 'workflow_dispatch') {
    const n = parseInt(process.env.ISSUE_NUMBER_INPUT, 10);
    if (!Number.isInteger(n) || n <= 0) throw new Error(`Bad issue_number input: ${process.env.ISSUE_NUMBER_INPUT}`);
    await evaluate(n, 'dispatch', context.payload.sender?.login);
    return;
  }

  await evaluate(context.payload.issue.number, context.payload.action, context.payload.sender?.login);

  // ── The rules ────────────────────────────────────────────────────────────

  async function evaluate(issueNumber, action, sender) {
    // Always read the issue live; the event payload can be stale by the time
    // a queued run starts.
    const { data: issue } = await github.rest.issues.get({ owner, repo, issue_number: issueNumber });
    const labels = issue.labels.map((l) => l.name);
    // An `unlabeled` run only fires for LABEL (see the workflow `if:`), so the
    // event itself proves the label was there a moment ago.
    const gated = action === 'unlabeled' || labels.includes(LABEL);
    console.log(`Issue #${issueNumber} by ${issue.user.login} (${issue.state}) — ${action} by ${sender ?? '-'}, enforce=${enforce}`);

    // 0. Scope: open issues, plus closed issues the gate closed itself.
    if (issue.pull_request) return log('is a pull request — not ours');
    if (issue.state === 'closed' && !gated) return log('closed by someone else — not ours');

    // 1. Exempt authors: bots and anyone with triage or better.
    if (issue.user.type === 'Bot') return log('author is a bot — exempt');
    if (await isTrusted(issue.user.login)) return pass('author has triage+ on this repo');

    // 2. Overrides: a triage+ user reopening the issue or removing the label
    //    wants it open. Anyone else doing so just triggers a re-check.
    if ((action === 'reopened' || action === 'unlabeled') && sender && (await isTrusted(sender))) {
      return pass(`${sender} ${action === 'reopened' ? 'reopened it' : 'removed the label'} — override`, { sticky: true });
    }
    if (labels.includes(BYPASS_LABEL)) return pass(`carries ${BYPASS_LABEL}`);

    // 3. The rule: every required checklist item is ticked.
    const missing = missingChecks(issue.body);
    if (!missing.length) return pass('every checklist item is ticked');
    return fail(missing);

    // ── Outcomes ─────────────────────────────────────────────────────────

    async function pass(reason, { sticky = false } = {}) {
      console.log(`PASS: ${reason}`);
      if (sticky) await addLabel(issueNumber, BYPASS_LABEL);
      if (issue.state === 'closed') {
        await mutate(`reopen issue #${issueNumber}`, () => github.rest.issues.update({ owner, repo, issue_number: issueNumber, state: 'open' }));
      }
      if (gated) {
        await removeLabel(issueNumber, LABEL);
        await deleteGateComment(issueNumber);
      }
    }

    async function fail(missingItems) {
      console.log(`FAIL: ${missingItems.length} of ${REQUIRED_CHECKS.length} checklist items not ticked`);
      await addLabel(issueNumber, LABEL);
      await upsertGateComment(issueNumber, closedComment(missingItems));
      if (issue.state === 'open') {
        await mutate(`close issue #${issueNumber}`, () =>
          github.rest.issues.update({ owner, repo, issue_number: issueNumber, state: 'closed', state_reason: 'not_planned' }),
        );
      }
    }

    function log(msg) {
      console.log(msg);
    }
  }

  // ── Comment text ─────────────────────────────────────────────────────────

  function closedComment(missingItems) {
    return [
      MARKER,
      "This issue has been closed automatically because its checklist is incomplete. This repo only keeps issues open when they come from a maintainer, or when the reporter confirms every item on the checklist in our issue templates. These aren't ticked yet:",
      '',
      ...missingItems.map((c) => `- ${c}`),
      '',
      "**Edit the issue description and tick the missing boxes, and the issue reopens on its own.** There's no need to file a new one. If the description has no checklist (for example, because it was filed from the command line), paste this at the end and tick each box:",
      '',
      '```markdown',
      '### Checklist',
      '',
      ...REQUIRED_CHECKS.map((c) => `- [ ] ${c}`),
      '```',
      '',
      `Only tick a box if it's true. If one doesn't apply, the report probably belongs somewhere else: questions and ideas go to [GitHub Discussions](https://github.com/${owner}/${repo}/discussions), and security reports follow [SECURITY.md](https://github.com/${owner}/${repo}/blob/main/SECURITY.md).`,
      '',
      `[CONTRIBUTING.md](${contributingUrl}) has the full reasoning, but in short: we're a small team, and a growing share of the issues we receive are speculative, or generated by AI tools without a person who has hit the problem. The checklist keeps the tracker focused on real problems.`,
      '',
      `*Maintainers: reopen, remove \`${LABEL}\`, or add \`${BYPASS_LABEL}\` to override.*`,
    ].join('\n');
  }

  // ── Helpers ──────────────────────────────────────────────────────────────

  async function mutate(description, fn) {
    if (!enforce) {
      console.log(`[dry-run] would ${description}`);
      return undefined;
    }
    return fn();
  }

  // Triage-or-better on this repo, from the permission endpoint's capability
  // flags (role names can be custom; author_association hides private org
  // members). Only a nonexistent user 404s; any other error must throw rather
  // than be read as "untrusted", or a maintainer's issue could be closed.
  async function isTrusted(username) {
    try {
      const { data } = await github.rest.repos.getCollaboratorPermissionLevel({ owner, repo, username });
      const p = data.user?.permissions;
      if (!p) throw new Error(`permission response for ${username} has no capability flags`);
      const trusted = Boolean(p.triage || p.push || p.maintain || p.admin);
      console.log(`  ${username}: ${trusted ? 'trusted' : 'not trusted'} (role ${data.role_name || '-'})`);
      return trusted;
    } catch (e) {
      if (e.status === 404) return false;
      throw new Error(`Permission check failed for ${username} (HTTP ${e.status ?? '?'}): ${e.message}`);
    }
  }

  async function addLabel(issueNumber, name) {
    await mutate(`add "${name}" to issue #${issueNumber}`, async () => {
      await ensureLabelExists(name);
      await github.rest.issues.addLabels({ owner, repo, issue_number: issueNumber, labels: [name] });
    });
  }

  async function removeLabel(issueNumber, name) {
    await mutate(`remove "${name}" from issue #${issueNumber}`, async () => {
      try {
        await github.rest.issues.removeLabel({ owner, repo, issue_number: issueNumber, name });
      } catch (e) {
        if (e.status !== 404) throw e;
      }
    });
  }

  async function ensureLabelExists(name) {
    const meta = {
      [LABEL]: ['b76e79', 'Auto-closed: issue checklist incomplete (see CONTRIBUTING.md)'],
      [BYPASS_LABEL]: ['0e8a16', 'Maintainer override for the issue checklist gate'],
    }[name];
    try {
      await github.rest.issues.getLabel({ owner, repo, name });
    } catch (e) {
      if (e.status !== 404) throw e;
      try {
        await github.rest.issues.createLabel({ owner, repo, name, color: meta[0], description: meta[1] });
      } catch (createErr) {
        if (createErr.status !== 422) throw createErr; // created concurrently
      }
    }
  }

  // The gate keeps at most one comment per issue: authored by the Actions bot
  // and carrying MARKER. It's created or updated on failure and deleted on pass.
  async function findGateComment(issueNumber) {
    const comments = await github.paginate(github.rest.issues.listComments, { owner, repo, issue_number: issueNumber, per_page: 100 });
    return comments.find((c) => c.user?.login === BOT_LOGIN && c.body?.includes(MARKER));
  }

  async function upsertGateComment(issueNumber, body) {
    const existing = await findGateComment(issueNumber);
    if (!existing) {
      await mutate(`comment on issue #${issueNumber}`, () => github.rest.issues.createComment({ owner, repo, issue_number: issueNumber, body }));
    } else if (existing.body !== body) {
      await mutate(`update the gate comment on issue #${issueNumber}`, () => github.rest.issues.updateComment({ owner, repo, comment_id: existing.id, body }));
    }
  }

  async function deleteGateComment(issueNumber) {
    const existing = await findGateComment(issueNumber);
    if (!existing) return;
    await mutate(`delete the gate comment on issue #${issueNumber}`, async () => {
      try {
        await github.rest.issues.deleteComment({ owner, repo, comment_id: existing.id });
      } catch (e) {
        if (e.status !== 404) throw e; // already deleted by a concurrent run
      }
    });
  }
};

// The REQUIRED_CHECKS items not ticked in body. Issue forms render a ticked
// box as `- [X] <label>`; matching ignores case, bullet style, and runs of
// whitespace, so a hand-pasted checklist counts too.
function missingChecks(body) {
  const norm = (s) => s.replace(/\s+/g, ' ').trim().toLowerCase();
  const ticked = new Set();
  for (const line of (body || '').split(/\r?\n/)) {
    const m = line.match(/^\s*[-*+]\s+\[[xX]\]\s+(.*)$/);
    if (m) ticked.add(norm(m[1]));
  }
  return REQUIRED_CHECKS.filter((c) => !ticked.has(norm(c)));
}

module.exports.REQUIRED_CHECKS = REQUIRED_CHECKS;
module.exports.missingChecks = missingChecks;
