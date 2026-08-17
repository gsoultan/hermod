import { describe, expect, it } from 'vitest';
import { looksLikeConnectionUrl, parseConnectionUrl } from '../lib/connectionUrl';

describe('parseConnectionUrl', () => {
  it('fills every field from a full postgres URL', () => {
    expect(
      parseConnectionUrl('postgres://ada:s3cret@db.example.com:5433/orders?sslmode=require'),
    ).toEqual({
      host: 'db.example.com',
      port: '5433',
      user: 'ada',
      password: 's3cret',
      database: 'orders',
      sslmode: 'require',
    });
  });

  it('returns only what the URL carries, so merging never blanks fields', () => {
    expect(parseConnectionUrl('mysql://db.example.com/shop')).toEqual({
      host: 'db.example.com',
      database: 'shop',
    });
  });

  it('decodes percent-encoded credentials', () => {
    expect(parseConnectionUrl('postgres://user%40corp:p%40ss@h/db')).toMatchObject({
      user: 'user@corp',
      password: 'p@ss',
    });
  });

  it('reads the SQL Server database query parameter', () => {
    expect(
      parseConnectionUrl('sqlserver://sa:pw@10.0.0.5:1433?database=master'),
    ).toMatchObject({ host: '10.0.0.5', port: '1433', database: 'master' });
  });

  it('keeps the whole mongodb URI, which has its own config key', () => {
    const uri = 'mongodb+srv://u:p@cluster0.example.mongodb.net/app';
    expect(parseConnectionUrl(uri)).toMatchObject({ uri, database: 'app' });
  });

  it('rejects things that are not connection URLs', () => {
    expect(parseConnectionUrl('localhost')).toBeNull();
    expect(parseConnectionUrl('https://example.com')).toBeNull();
    expect(parseConnectionUrl('SELECT * FROM users')).toBeNull();
    expect(parseConnectionUrl('')).toBeNull();
  });
});

describe('looksLikeConnectionUrl', () => {
  it('recognises known schemes and only those', () => {
    expect(looksLikeConnectionUrl('postgres://x')).toBe(true);
    expect(looksLikeConnectionUrl('mongodb+srv://x')).toBe(true);
    expect(looksLikeConnectionUrl('clickhouse://x')).toBe(true);
    expect(looksLikeConnectionUrl('ftp://x')).toBe(false);
    expect(looksLikeConnectionUrl('not a url')).toBe(false);
  });
});
