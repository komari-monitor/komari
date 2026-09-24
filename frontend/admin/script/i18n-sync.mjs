#!/usr/bin/env node
import fs from 'fs';
import path from 'path';
import process from 'process';
import { fileURLToPath } from 'url';
import dotenv from 'dotenv';

// Load env from .env if present
dotenv.config();

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

// Config
const LOCALES_DIR = process.env.I18N_LOCALES_DIR || path.resolve(__dirname, '../src/i18n/locales');
const SOURCE_LOCALE = process.env.I18N_SOURCE || 'zh_CN';
const OPENAI_API_KEY = process.env.OPENAI_API_KEY || process.env.OPENAI_API_TOKEN || '';
const OPENAI_BASE_URL = process.env.OPENAI_BASE_URL || process.env.OPENAI_API_BASE || process.env.OPENAI_ENDPOINT || '';
const OPENAI_MODEL = process.env.OPENAI_MODEL || 'gpt-4o-mini';
const DRY_RUN = process.argv.includes('--dry-run');
const FILL_AI = !process.argv.includes('--no-ai');

function getArg(name, def = undefined) {
  // supports --name=value
  const hit = process.argv.find(a => a.startsWith(name + '='));
  if (!hit) return def;
  const v = hit.split('=')[1];
  return v ?? def;
}

// Batching config: either limit by number of keys or by input char size approximation
const AI_BATCH_SIZE = parseInt(process.env.I18N_AI_BATCH_SIZE || getArg('--ai-batch-size', '50'), 10);
const AI_MAX_INPUT_CHARS = parseInt(process.env.I18N_AI_MAX_INPUT_CHARS || getArg('--ai-max-chars', '12000'), 10);

function isObject(v) {
  return v && typeof v === 'object' && !Array.isArray(v);
}

function flatten(obj, prefix = '') {
  const out = {};
  for (const [k, v] of Object.entries(obj || {})) {
    const key = prefix ? `${prefix}.${k}` : k;
    if (isObject(v)) Object.assign(out, flatten(v, key));
    else out[key] = v;
  }
  return out;
}

function unflatten(flat) {
  const root = {};
  for (const [key, value] of Object.entries(flat)) {
    const parts = key.split('.');
    let cur = root;
    parts.forEach((p, idx) => {
      if (idx === parts.length - 1) cur[p] = value;
      else cur[p] = cur[p] ?? {} , cur = cur[p];
    });
  }
  return root;
}

function sortKeysDeep(obj) {
  if (!isObject(obj)) return obj;
  const sorted = {};
  for (const key of Object.keys(obj).sort((a, b) => a.localeCompare(b))) {
    sorted[key] = sortKeysDeep(obj[key]);
  }
  return sorted;
}

function readJson(fp) {
  try { return JSON.parse(fs.readFileSync(fp, 'utf8')); } catch (_) { return {}; }
}

function writeJson(fp, data) {
  const content = JSON.stringify(data, null, 2) + '\n';
  if (DRY_RUN) return { wrote: false, content };
  fs.writeFileSync(fp, content, 'utf8');
  return { wrote: true, content };
}

function getLocalePath(locale) {
  return path.join(LOCALES_DIR, `${locale}.json`);
}

function approxPairsCharLen(pairs) {
  // Rough estimate for JSON size to avoid too large prompts
  let len = 2; // braces
  for (const { key, sourceText } of pairs) {
    const k = String(key);
    const v = String(sourceText ?? '');
    // quotes + colon + comma overhead ~6 chars
    len += k.length + v.length + 6;
  }
  return len;
}

function chunkPairs(pairs) {
  if (!pairs.length) return [];
  const chunks = [];
  let cur = [];
  let curLen = 0;
  for (const p of pairs) {
    const est = p.key.length + String(p.sourceText ?? '').length + 6;
    const willLen = (cur.length ? curLen + est : 2 + est); // braces present if starting
    const sizeOver = cur.length >= AI_BATCH_SIZE || (AI_MAX_INPUT_CHARS > 0 && willLen > AI_MAX_INPUT_CHARS);
    if (cur.length && sizeOver) {
      chunks.push(cur);
      cur = [p];
      curLen = 2 + est;
    } else {
      cur.push(p);
      curLen = willLen;
    }
  }
  if (cur.length) chunks.push(cur);
  return chunks;
}

async function aiTranslateBatch(pairs, targetLocale) {
  // pairs: Array<{key, sourceText}>
  if (!FILL_AI) return pairs.map(({ key }) => ({ key, text: '' }));
  if (!OPENAI_API_KEY || !OPENAI_MODEL) return pairs.map(({ key }) => ({ key, text: '' }));

  // Build prompt to translate values only
  const system = `You are a translation engine. Translate from Simplified Chinese (zh-CN) to ${targetLocale.replace('_', '-')}. Return ONLY a compact JSON object mapping keys to translated strings. Keep placeholders like {name} or {{count}} intact. If you are unsure, return an empty string for that key.`;
  const inputMap = {};
  for (const { key, sourceText } of pairs) inputMap[key] = String(sourceText ?? '');
  const user = JSON.stringify(inputMap);

  try {
    const endpoint = (OPENAI_BASE_URL || 'https://api.openai.com/v1').replace(/\/$/, '') + '/chat/completions';
    // retry up to 3x on rate limit or transient errors
    let lastErr; let data;
    for (let attempt = 0; attempt < 3; attempt++) {
      const res = await fetch(endpoint, {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
          'Authorization': `Bearer ${OPENAI_API_KEY}`,
        },
        body: JSON.stringify({
          model: OPENAI_MODEL,
          messages: [
            { role: 'system', content: system },
            { role: 'user', content: user },
          ],
          temperature: 0.2,
        }),
      });
      if (res.ok) { data = await res.json(); lastErr = undefined; break; }
      lastErr = new Error(`AI HTTP ${res.status}`);
      const retryAfter = parseInt(res.headers.get('retry-after') || '0', 10) || 1 + attempt * 2;
      await new Promise(r => setTimeout(r, retryAfter * 1000));
    }
    if (!data) throw lastErr || new Error('AI unknown error');
    const content = data.choices?.[0]?.message?.content || '{}';
    let parsed = {};
    try { parsed = JSON.parse(content); } catch (_) { parsed = {}; }
    return pairs.map(({ key }) => ({ key, text: typeof parsed[key] === 'string' ? parsed[key] : '' }));
  } catch (e) {
    console.warn('AI translate failed:', e.message || e);
    return pairs.map(({ key }) => ({ key, text: '' }));
  }
}

async function aiTranslateAll(pairs, targetLocale) {
  // Split pairs into multiple requests based on configured batch limits
  const chunks = chunkPairs(pairs);
  const out = [];
  for (let i = 0; i < chunks.length; i++) {
    const part = chunks[i];
    if (DRY_RUN) console.log(`[AI] translating chunk ${i + 1}/${chunks.length} (${part.length} keys)`);
    const r = await aiTranslateBatch(part, targetLocale);
    out.push(...r);
  }
  return out;
}

function diffAndMerge(sourceFlat, targetFlat) {
  const result = {};
  const added = [];
  const updated = [];
  const removed = [];

  // Keep only keys that exist in source; copy target value if present (even empty), otherwise mark as added but DO NOT insert placeholder
  for (const key of Object.keys(sourceFlat)) {
    if (key in targetFlat) {
      const val = targetFlat[key];
      if (typeof val === 'string' && val.trim() === '') {
        // treat empty as missing, don't include now
        added.push(key);
      } else {
        result[key] = val;
      }
    } else {
      added.push(key);
    }
  }
  // Keys that exist in target but not in source should be removed
  for (const key of Object.keys(targetFlat)) {
    if (!(key in sourceFlat)) removed.push(key);
  }
  return { result, added, removed, updated };
}

function batchPairs(keys, sourceFlat, targetFlat) {
  const missing = [];
  for (const key of keys) {
    const cur = targetFlat[key];
    if (cur === undefined || cur === null || String(cur).trim() === '') {
      missing.push({ key, sourceText: sourceFlat[key] });
    }
  }
  return missing;
}

async function main() {
  const localeFiles = fs.readdirSync(LOCALES_DIR).filter(f => f.endsWith('.json'));
  const locales = localeFiles.map(f => path.basename(f, '.json'));
  if (!locales.includes(SOURCE_LOCALE)) {
    console.error(`Source locale ${SOURCE_LOCALE} not found in ${LOCALES_DIR}`);
    process.exit(1);
  }

  const sourcePath = getLocalePath(SOURCE_LOCALE);
  const sourceObj = readJson(sourcePath);
  const sourceSorted = sortKeysDeep(sourceObj);
  const sourceChanged = JSON.stringify(sourceObj) !== JSON.stringify(sourceSorted);
  if (!DRY_RUN && sourceChanged) {
    writeJson(sourcePath, sourceSorted);
    console.log(`Source ${SOURCE_LOCALE}: updated (sorted keys)`);
  } else {
    console.log(`Source ${SOURCE_LOCALE}: ${sourceChanged ? 'would update (sorted keys)' : 'no change'}`);
  }
  const sourceFlat = flatten(sourceSorted);

  for (const locale of locales) {
    if (locale === SOURCE_LOCALE) continue;
    const targetPath = getLocalePath(locale);
    const targetObj = readJson(targetPath);
    const targetFlat = flatten(targetObj);

    const { result, added, removed } = diffAndMerge(sourceFlat, targetFlat);

    // AI fill for missing/empty
    // find missing from source perspective (not present in target or existing but blank)
    const missingPairs = batchPairs(Object.keys(sourceFlat), sourceFlat, targetFlat);
    let aiFilled = [];
    if (missingPairs.length > 0) {
      aiFilled = await aiTranslateAll(missingPairs, locale);
      for (const { key, text } of aiFilled) {
        if (typeof text === 'string' && text.trim() !== '') {
          result[key] = text; // only add when AI returns non-empty
        }
      }
    }

    // Unflatten + sort
    const merged = sortKeysDeep(unflatten(result));

    const before = JSON.stringify(sortKeysDeep(targetObj));
    const after = JSON.stringify(merged);
    const hasChange = before !== after;

    const changes = {
      locale,
      added,
      removed,
      aiFilled: aiFilled.filter(x => x.text).map(x => x.key),
      stillEmpty: aiFilled.filter(x => !x.text).map(x => x.key),
    };

  if (!DRY_RUN && hasChange) writeJson(targetPath, merged);

  console.log(`Locale ${locale}: ${DRY_RUN ? (hasChange ? 'would update' : 'no change') : (hasChange ? 'updated' : 'no change')}`);
    if (added.length || removed.length || missingPairs.length) {
      console.log(JSON.stringify(changes, null, 2));
    }
  }
}

main().catch(e => {
  console.error(e);
  process.exit(1);
});
