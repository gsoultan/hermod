// Transactional sink group (two-phase commit) editor logic.
//
// The group is stored as a sink whose config names its members by id, so the
// editor's whole job is turning a comma-separated list into choices a person can
// make, and refusing the choices that cannot work.
//
// The refusals matter more than the choices. A group whose members cannot take
// part in two-phase commit does not fail when it is saved — it fails when the
// workflow next runs, which may be days later and is exactly when nobody wants
// to discover it.

/** A sink as the editor knows it. */
export type SinkOption = {
  id: string;
  name: string;
  type: string;
};

/** Parse the stored comma-separated member list. */
export function parseMembers(raw: unknown): string[] {
  if (typeof raw !== 'string') return [];
  return raw
    .split(',')
    .map((id) => id.trim())
    .filter(Boolean);
}

/** Render the member list back into the stored form. */
export function formatMembers(ids: string[]): string {
  return ids.filter(Boolean).join(',');
}

/**
 * Which sinks may be offered as members.
 *
 * `capableTypes` comes from the backend rather than being listed here, because
 * only one of the two can be authoritative and it is not the browser. A group
 * itself is excluded: nesting one inside another is not a thing the coordinator
 * models, and the group being edited would otherwise offer itself.
 */
export function eligibleSinks(
  sinks: SinkOption[],
  capableTypes: string[],
  currentSinkId?: string,
): SinkOption[] {
  const capable = new Set(capableTypes);
  return (sinks || []).filter(
    (s) => s && s.id !== currentSinkId && s.type !== 'txgroup' && capable.has(s.type),
  );
}

/**
 * Sinks the user might expect to see but cannot pick, with the reason.
 *
 * Silently omitting them is worse than it sounds: someone who has just created a
 * Kafka sink and cannot find it in the list has no way to tell whether they made
 * a mistake or the feature is broken.
 */
export function ineligibleSinks(
  sinks: SinkOption[],
  capableTypes: string[],
  currentSinkId?: string,
): SinkOption[] {
  const capable = new Set(capableTypes);
  return (sinks || []).filter(
    (s) => s && s.id !== currentSinkId && s.type !== 'txgroup' && !capable.has(s.type),
  );
}

/** A duration the engine will accept, e.g. "15m", "1h30m", "90s". */
const DURATION = /^(\d+(\.\d+)?(ns|us|µs|ms|s|m|h))+$/;

export function isValidPreparedAge(raw: string): boolean {
  const value = (raw || '').trim();
  if (value === '') return true; // optional; the engine supplies a default
  return DURATION.test(value);
}

export type TxGroupProblem = { field: 'members' | 'max_prepared_age'; message: string };

/**
 * Everything wrong with the group as configured, in the order a person would fix
 * it. Empty means it will start.
 */
export function validateTxGroup(config: Record<string, any>, sinks: SinkOption[]): TxGroupProblem[] {
  const problems: TxGroupProblem[] = [];
  const members = parseMembers(config?.members);

  if (members.length < 2) {
    problems.push({
      field: 'members',
      message:
        'Choose at least two sinks. A single sink already gets atomicity from its own ' +
        'transaction, so a group of one adds the cost of two-phase commit and nothing else.',
    });
  }

  const known = new Set((sinks || []).map((s) => s.id));
  const missing = members.filter((id) => !known.has(id));
  if (missing.length > 0) {
    problems.push({
      field: 'members',
      message: `${missing.length === 1 ? 'A member no longer exists' : 'Members no longer exist'}: ${missing.join(
        ', ',
      )}. The group will refuse to start until they are removed or restored.`,
    });
  }

  if (!isValidPreparedAge(config?.max_prepared_age ?? '')) {
    problems.push({
      field: 'max_prepared_age',
      message:
        'Use a Go duration such as 15m, 1h or 90s. Anything else is rejected when the workflow starts.',
    });
  }

  return problems;
}
