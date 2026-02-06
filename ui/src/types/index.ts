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
  created_at?: string;
}

export interface LogEntry {
  timestamp: string;
  level: 'INFO' | 'WARN' | 'ERROR' | 'DEBUG';
  message: string;
  node_id?: string;
  data?: any;
}
