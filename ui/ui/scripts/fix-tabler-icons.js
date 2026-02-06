// Broad clean-up for Tabler icon imports across UI
// - Removes invalid lines like: `import  from '@tabler/icons-react';`
// - Removes any existing Tabler named imports
// - Rebuilds a single named import with exactly the icons used in the file
// Usage: from ui folder run: node scripts/fix-tabler-icons.js

import { promises as fs } from 'fs';
import path from 'path';
import { fileURLToPath } from 'url';

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);
const UI_DIR = path.resolve(__dirname, '..');
const SRC_DIR = path.join(UI_DIR, 'src');

/** Recursively collect all files under dir that match ext regex */
async function collectFiles(dir, exts = new Set(['.ts', '.tsx'])) {
  const out = [];
  async function walk(d) {
    const entries = await fs.readdir(d, { withFileTypes: true });
    for (const e of entries) {
      const p = path.join(d, e.name);
      if (e.isDirectory()) {
        await walk(p);
      } else if (exts.has(path.extname(e.name))) {
        out.push(p);
      }
    }
  }
  await walk(dir);
  return out;
}

function rebuildTablerImport(content) {
  const original = content;
  // Remove invalid blank imports
  const invalidImportRe = /^\s*import\s+from\s+['"]@tabler\/icons-react['"];?\s*$(?:\r?\n)?/gm;
  content = content.replace(invalidImportRe, '');

  // Remove existing Tabler named imports (we will recreate)
  const namedImportRe = /^\s*import\s*\{[^}]*\}\s*from\s*['"]@tabler\/icons-react['"];?\s*$(?:\r?\n)?/gm;
  content = content.replace(namedImportRe, '');

  // Find used Icon components e.g., <IconTrash .../>, icon={<IconClock .../>}
  const icons = new Set();
  // JSX tags
  const iconJsxRe = /<\s*(Icon[A-Z][A-Za-z0-9_]*)\b/g;
  let m;
  while ((m = iconJsxRe.exec(content)) !== null) {
    const name = m[1];
    if (name && name !== 'Icon') icons.add(name);
  }
  // Variable references (e.g., ICON_MAP: { IconGitBranch, IconSearch })
  const iconVarRe = /\b(Icon[A-Z][A-Za-z0-9_]*)\b/g;
  while ((m = iconVarRe.exec(content)) !== null) {
    const name = m[1];
    if (name && name !== 'Icon') icons.add(name);
  }

  if (icons.size === 0) {
    // Nothing to import; return possibly cleaned content
    return { changed: content !== original, content };
  }

  const importLine = `import { ${Array.from(icons).sort().join(', ')} } from '@tabler/icons-react';\n`;

  // Insert after the last leading import statement block
  const importStatements = Array.from(content.matchAll(/^import\s+.*;\s*\r?\n?/gm));
  if (importStatements.length > 0) {
    const last = importStatements[importStatements.length - 1];
    const idx = last.index + last[0].length;
    content = content.slice(0, idx) + importLine + content.slice(idx);
  } else {
    content = importLine + content;
  }

  return { changed: content !== original, content };
}

async function main() {
  const files = await collectFiles(SRC_DIR);
  let changed = 0;
  for (const f of files) {
    let text = await fs.readFile(f, 'utf8');
    const { changed: didChange, content } = rebuildTablerImport(text);
    if (didChange) {
      await fs.writeFile(f, content, 'utf8');
      changed++;
      // eslint-disable-next-line no-console
      console.log(`Updated: ${path.relative(UI_DIR, f)}`);
    }
  }
  // eslint-disable-next-line no-console
  console.log(`Tabler icon import fix complete. Files updated: ${changed}`);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
