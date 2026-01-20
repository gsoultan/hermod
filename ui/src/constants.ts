export const API_BASE = '/api';

export interface Source {
  id: string;
  name: string;
  type: string;
  vhost: string;
  config: Record<string, any>;
}

export interface Sink {
  id: string;
  name: string;
  type: string;
  vhost: string;
  config: Record<string, any>;
}
