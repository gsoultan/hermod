/**
 * Humanizes technical error messages from the backend into actionable advice for non-technical users.
 */
export function humanizeError(error: any): { title: string; message: string; color: string } {
  const msg = typeof error === 'string' ? error : (error?.message || 'An unexpected error occurred');
  
  // connection timeouts
  if (msg.includes('deadline exceeded') || msg.includes('timeout')) {
    return {
      title: 'Connection Timed Out',
      message: 'Hermod could not reach the server in time. Check if the hostname/IP is correct and that your firewall allows connections on the specified port.',
      color: 'red'
    };
  }

  // Postgres specific
  if (msg.includes('pgbouncer') || msg.includes('pooler')) {
    return {
      title: 'PgBouncer Bottleneck',
      message: 'Your connection is through PgBouncer and is slow. Try adding "pgbouncer=true" to your connection string to use the faster simple protocol.',
      color: 'orange'
    };
  }

  if (msg.includes('wal_level')) {
    return {
      title: 'CDC Not Enabled',
      message: 'The database is not configured for CDC. You must set "wal_level = logical" in postgresql.conf and restart the database.',
      color: 'red'
    };
  }

  if (msg.includes('replication privileges')) {
    return {
      title: 'Permission Denied',
      message: 'The database user does not have replication permissions. Run "ALTER USER your_user REPLICATION;" on the server.',
      color: 'red'
    };
  }

  // Active workflow blocks (from our new checks)
  if (msg.includes('active workflow')) {
    return {
      title: 'Resource Locked',
      message: 'This resource cannot be modified because it is currently being used by a running workflow. Please stop the workflow first.',
      color: 'orange'
    };
  }

  // Authentication
  if (msg.includes('password authentication failed') || msg.includes('invalid password')) {
    return {
      title: 'Authentication Failed',
      message: 'The username or password you provided is incorrect. Double-check your credentials.',
      color: 'red'
    };
  }

  // Default
  return {
    title: 'Operation Failed',
    message: msg,
    color: 'red'
  };
}
