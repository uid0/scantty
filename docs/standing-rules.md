# Standing rules

This is the roster that "standing rule N", "house rule N" and a bare "rule N"
cite everywhere in this repository — AGENTS.md, doc comments, test names and
failure messages. A citation names a number; this file is what the number says.

`internal/doccheck/standing_rules_test.go` fails a citation of a number this
file does not define, a citation that spells the number as an English
ordinal instead, a gap or repeat in the numbering below, and an AGENTS.md
that stops pointing here. It cannot tell whether a citation names the RIGHT
rule for its sentence; that is still a reading.

## Where this text came from

The numbered rules were used in the task instructions for work on this project
from 2026-08-24, and for weeks no file in this repository defined them; nothing
in its git history ever carried them. This file records them verbatim from
those instructions, which carried the roster between 2026-08-24 and 2026-09-11.

Each rule's heading below is a wording that appears verbatim in several sets of
those instructions. Some added a sentence of elaboration; the ones that are
about the rule rather than about one task are quoted under it, verbatim except
that wording specific to how the work was supervised has been neutralised. The
numbering was stable throughout: no number was ever reused for a different
rule. Rules 1–8 were in use from 2026-08-24, rules 9 and 10 from 2026-08-25 and
rule 11 from 2026-08-26. The rules are spelled with small variations in
emphasis — "action bar or help line" against "action bar", "operator input"
against "what the operator typed" — and none of those variations changes what
the rule forbids.

Nothing here is a new rule, and nothing here changes a rule's substance. A
change to a rule is the project owner's decision, not an edit to this file.

## 1. A keypress must always produce a distinguishable operator-visible change.

> A key that acts invisibly is the same defect as a key that does nothing -
> arguably worse, because the operator has no reason to suspect it.

## 2. An action bar or help line must never name a key that will not act at that moment, and never omit one that will.

> If a key stops acting at a refused height, the bar must stop naming it there.

## 3. Never conflate "found nothing" with "could not tell".

> they are different facts and the operator acts differently on each.

## 4. Never silently discard or overwrite what the operator typed.

> If input is about to be dropped, say so first.

## 5. Every width is measured in DISPLAY CELLS, never runes, and every bound computed in a single pass. 80 columns must HOLD.

> 80 columns is the width that must HOLD, not the width to render as though
> you had - derive room from the live terminal width and never discard data
> the terminal had room to show.

The instructions also applied it to the terminal's HEIGHT ("the frame must fit the
terminal HEIGHT"), and this repository cites "rule 5's width form" in that
sense. What 80 columns means today — the size Root refuses to draw below — is
`internal/tui/layout.go`'s to say.

## 6. A bound expressed in terms of an unbounded value is not a bound.

> Never leave a truncated value looking complete.

> Every part of a fixed row is either a bounded identifier or a fact that never
> gives. On a confirm surface the facts being confirmed survive whole and the
> identifier abbreviates, visibly marked - never leave a truncated value
> looking complete, and never leave a truncated number looking like a real
> number.

## 7. Derive, never enumerate.

> A hand-maintained list of screens, phases or keys is how several defects
> reached review; derive the set from its authoritative source and make an
> omission fail the build.

## 8. A documented claim the code does not honour is itself a defect.

> Make the behaviour true, or delete the claim - never soften the sentence to
> match weaker behaviour.

Later wordings add "in either direction": a sentence describing a loss that no
longer happens is the same defect as one promising behaviour that never did.

## 9. No new check unless you have watched it fail.

> Under pressure to make a round pass, a check gets weakened to whatever is
> easy to assert, and the weakening is invisible because the test still passes.
> Every check you add or modify here must be demonstrated failing against a
> deliberately broken version of what it protects.

## 10. Derive the set of sites a rule applies to, from what the rule is ABOUT rather than from where you are editing.

> Three separate defects came from applying a rule by hand to the sites someone
> thought of and missing one. Obtain the set from its authoritative source and
> make an omission fail the build.

## 11. A refusal is only legitimate when the operator can act on it.

> A refusal they cannot satisfy is a dead end wearing a safety costume.

This repository applies it past refusals in the narrow sense, to any surface
that promises the operator something no key on the frame can deliver — a
`↓ N more below` marker over a bar naming no key that fetches it, or a dead-end
state whose way out is unnamed.
