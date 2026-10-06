#!/usr/bin/env node
// sync-www.mjs — build the Capacitor web bundle: copy the Reach phone app
// (apps/reach/ui/static) into ./www and the shared UI kit (shared/ui) into
// ./www/shared, which is where the app's `./shared/…` imports resolve. The Go
// agent mounts the same two directories the same way when it serves the PWA
// (apps/reach/win/overlayfs.go), so the app is byte-identical in both.
import { cpSync, mkdirSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, '..', '..', '..');
const app = join(root, 'apps', 'reach', 'ui', 'static');
const shared = join(root, 'shared', 'ui');
const www = join(here, 'www');

const keep = (src) => !/(^|\/)(README\.md|demo\.html|demo\.js|ui\.go|\.DS_Store)$/.test(src);

rmSync(www, { recursive: true, force: true });
mkdirSync(www, { recursive: true });
cpSync(app, www, { recursive: true, filter: keep });
cpSync(shared, join(www, 'shared'), { recursive: true, filter: keep });

const version = readFileSync(join(root, 'apps', 'reach', 'VERSION'), 'utf8').trim();
writeFileSync(join(www, 'version.json'), JSON.stringify({ version }) + '\n');
console.log(`www/ ← ${app} + ${shared} (Reach ${version})`);
