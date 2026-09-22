export interface Source {
  id: string;
  name: string;
  type: string;
  vhost: string;
  config: Record<string, any>;
  sample?: string;
  active?: boolean;
  status?: string;
  worker_id?: string;
  // storage.Source has always carried this and the API has always round-tripped
  // it; the type omitted it, so no form could set it.
  workspace_id?: string;
}

export interface Sink {
  id: string;
  name: string;
  type: string;
  vhost: string;
  config: Record<string, any>;
  active?: boolean;
  status?: string;
  worker_id?: string;
  // See Source.workspace_id.
  workspace_id?: string;
}

export interface Workflow {
  id: string;
  name: string;
  active: boolean;
  status: string;
  nodes?: any[];
  edges?: any[];
  vhost?: string;
  workspace_id?: string;
  created_at?: string;
  updated_at?: string;
  worker_id?: string;
}

export interface Worker {
  id: string;
  name: string;
  host?: string;
  port?: number;
  description?: string;
  status: string;
  vhost?: string;
  last_seen?: string;
  cpu_usage?: number;
  memory_usage?: number;
  draining?: boolean;
}

export interface VHost {
  id: string;
  name: string;
  description?: string;
}

export type Role = 'Administrator' | 'Editor' | 'Viewer';

export interface User {
  id: string;
  username: string;
  full_name: string;
  email: string;
  role: Role;
  vhosts: string[];
  two_factor_enabled: boolean;
  password?: string;
  created_at?: string;
  last_login?: string;
}

export interface Workspace {
  id: string;
  name: string;
  description?: string;
  vhost?: string;
  // The four quotas the API has always returned (storage.Workspace) but this
  // type omitted, so nothing in the UI could read them back after creation.
  // 0 means unlimited, which is also what an absent field means.
  max_workflows?: number;
  max_cpu?: number;
  max_memory?: number;
  max_throughput?: number;
  created_at?: string;
}

export interface LogEntry {
  timestamp: string;
  level: 'INFO' | 'WARN' | 'ERROR' | 'DEBUG';
  message: string;
  node_id?: string;
  data?: any;
}
