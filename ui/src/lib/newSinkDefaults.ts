/**
 * Settings a sink created now starts with, where they differ from what the
 * backend assumes for a stored sink that never named them.
 *
 * The backend's defaults cannot move: a sink saved before a better default
 * existed has been relying on the old one, and so has whatever reads its
 * output. A new sink can start somewhere better, provided the setting is
 * written into its config — then the stored sink says what it does, rather than
 * inheriting whatever the default happens to be when it is next read.
 */
const NEW_SINK_DEFAULTS: Record<string, Record<string, string>> = {
  // An unset data_mode sends the whole row as one JSON string, and FCM accepts
  // 4096 bytes: a sink set up with the defaults failed on its first wide row.
  // Starting from "only the values listed" fits by construction.
  fcm: { data_mode: 'none' },
};

/** The defaults for a new sink of this type that its config does not already set. */
export function missingNewSinkDefaults(type: string, config: Record<string, unknown> | undefined): Record<string, string> {
  const defaults = NEW_SINK_DEFAULTS[type] ?? {};
  return Object.fromEntries(Object.entries(defaults).filter(([key]) => config?.[key] === undefined));
}
