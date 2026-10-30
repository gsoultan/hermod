// Sink nodes that commit together.
//
// A sink node joins a transaction by naming it in its config ("transaction").
// Every sink node of the workflow with the same name is a member, and when the
// workflow runs the server builds the members into one two-phase group, so one
// event's rows for them commit or roll back as one. This file is the editor's
// side of that rule: who is in a transaction, and what would make the server
// refuse it at save — a transaction with one member, or a member whose database
// cannot hold a transaction open for another.

import { typeLabel } from '@/utils/txgroup';

/** The sink node config key naming the transaction; the server reads the same one. */
export const TRANSACTION_KEY = 'transaction';

/** A node as the editor's store holds it: data is the node's config. */
export type EditorNode = {
  id: string;
  type?: string;
  data: Record<string, any>;
};

/** The transaction a sink node commits in, or '' for none. */
export function transactionOf(data: Record<string, any> | undefined | null): string {
  const raw = data?.[TRANSACTION_KEY];
  return typeof raw === 'string' ? raw.trim() : '';
}

const isSink = (n: EditorNode) => n.type === 'sink';

/** Every transaction name in the workflow, once each, in the order first seen. */
export function transactionNames(nodes: EditorNode[]): string[] {
  const seen: string[] = [];
  for (const n of nodes || []) {
    if (!isSink(n)) continue;
    const name = transactionOf(n.data);
    if (name && !seen.includes(name)) seen.push(name);
  }
  return seen;
}

/** The sink nodes that commit in the named transaction. */
export function membersOf(nodes: EditorNode[], name: string): EditorNode[] {
  if (!name) return [];
  return (nodes || []).filter((n) => isSink(n) && transactionOf(n.data) === name);
}

export type CommitTogetherProblem = {
  kind: 'unnamed' | 'alone' | 'incapable';
  message: string;
  /** The node at fault, when one is. */
  nodeId?: string;
};

/** How a node is named to a person: its label, or its id. */
export const nodeName = (n: EditorNode) => (n.data?.label as string) || n.id;

/**
 * Everything that would make the server refuse the transaction at save, in the
 * order a person would fix it. `capableTypes` is what the server said can take
 * part; null while that answer is still loading, when nothing is said about
 * types rather than guessing.
 */
export function commitTogetherProblems(
  nodes: EditorNode[],
  name: string,
  capableTypes: string[] | null,
): CommitTogetherProblem[] {
  if (!name) {
    return [{ kind: 'unnamed', message: 'Give the transaction a name so other sink nodes can join it.' }];
  }
  const members = membersOf(nodes, name);
  const problems: CommitTogetherProblem[] = [];
  if (members.length < 2) {
    problems.push({
      kind: 'alone',
      message:
        `No other sink node is in "${name}" yet. A database already commits its own rows alone, ` +
        'so the workflow cannot be saved until a second sink node joins or this is turned off.',
    });
  }
  if (capableTypes) {
    const capable = new Set(capableTypes);
    for (const m of members) {
      const type = String(m.data?.type ?? '');
      if (!capable.has(type)) {
        problems.push({
          kind: 'incapable',
          nodeId: m.id,
          message:
            `${nodeName(m)} is a ${typeLabel(type)} sink, which cannot hold a transaction open for ` +
            `another database. Only ${capableTypes.map(typeLabel).join(', ')} sinks can commit together.`,
        });
      }
    }
  }
  return problems;
}
