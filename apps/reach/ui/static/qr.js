// qr.js — a tiny QR encoder: byte mode, versions 1–4, error correction L,
// automatic mask. Enough for http://192.168.x.y:7788/ (≤ 78 bytes). Throws
// when the text does not fit so the caller can fall back to plain text.

const CAPACITY = [0, 17, 32, 53, 78];        // bytes, level L, versions 1–4
const DATA_CW = [0, 19, 34, 55, 80];          // data codewords (single block)
const EC_CW = [0, 7, 10, 15, 20];             // EC codewords per block
const ALIGN = { 2: 18, 3: 22, 4: 26 };        // alignment pattern centre

// GF(256) with the QR primitive polynomial 0x11D.
function gfMul(x, y) {
  let z = 0;
  for (let i = 7; i >= 0; i--) {
    z = (z << 1) ^ ((z >>> 7) * 0x11d);
    z ^= ((y >>> i) & 1) * x;
  }
  return z & 0xff;
}

function rsDivisor(degree) {
  const result = new Array(degree).fill(0);
  result[degree - 1] = 1;
  let root = 1;
  for (let i = 0; i < degree; i++) {
    for (let j = 0; j < result.length; j++) {
      result[j] = gfMul(result[j], root);
      if (j + 1 < result.length) result[j] ^= result[j + 1];
    }
    root = gfMul(root, 2);
  }
  return result;
}

function rsRemainder(data, divisor) {
  const result = new Array(divisor.length).fill(0);
  for (const b of data) {
    const factor = b ^ result.shift();
    result.push(0);
    for (let i = 0; i < divisor.length; i++) result[i] ^= gfMul(divisor[i], factor);
  }
  return result;
}

/** Encode text (UTF-8) into {size, modules: boolean[][]}. */
export function qrEncode(text) {
  const bytes = new TextEncoder().encode(text);
  let version = 0;
  for (let v = 1; v <= 4; v++) if (bytes.length <= CAPACITY[v]) { version = v; break; }
  if (!version) throw new Error(`QR: ${bytes.length} bytes does not fit in version 4-L (78)`);

  // Bit stream: mode 0100, 8-bit count, data, terminator, padding.
  const bits = [];
  const push = (val, n) => { for (let i = n - 1; i >= 0; i--) bits.push((val >>> i) & 1); };
  push(4, 4);
  push(bytes.length, 8);
  for (const b of bytes) push(b, 8);
  const capBits = DATA_CW[version] * 8;
  push(0, Math.min(4, capBits - bits.length));
  while (bits.length % 8) bits.push(0);
  for (let pad = 0xec; bits.length < capBits; pad ^= 0xec ^ 0x11) push(pad, 8);
  const data = [];
  for (let i = 0; i < bits.length; i += 8) data.push(parseInt(bits.slice(i, i + 8).join(''), 2));
  const codewords = data.concat(rsRemainder(data, rsDivisor(EC_CW[version])));

  const size = version * 4 + 17;
  const modules = Array.from({ length: size }, () => new Array(size).fill(false));
  const isFunction = Array.from({ length: size }, () => new Array(size).fill(false));
  const set = (x, y, dark) => { modules[y][x] = dark; isFunction[y][x] = true; };

  const finder = (cx, cy) => {
    for (let dy = -4; dy <= 4; dy++) for (let dx = -4; dx <= 4; dx++) {
      const x = cx + dx, y = cy + dy;
      if (x < 0 || y < 0 || x >= size || y >= size) continue;
      const d = Math.max(Math.abs(dx), Math.abs(dy));
      set(x, y, d !== 2 && d !== 4);
    }
  };
  finder(3, 3); finder(size - 4, 3); finder(3, size - 4);
  for (let i = 0; i < size; i++) { if (!isFunction[6][i]) set(i, 6, i % 2 === 0); if (!isFunction[i][6]) set(6, i, i % 2 === 0); }
  if (ALIGN[version]) {
    const c = ALIGN[version];
    for (let dy = -2; dy <= 2; dy++) for (let dx = -2; dx <= 2; dx++) set(c + dx, c + dy, Math.max(Math.abs(dx), Math.abs(dy)) !== 1);
  }
  // Reserve the format areas (filled after masking).
  for (let i = 0; i <= 8; i++) { if (!isFunction[8][i]) set(i, 8, false); if (!isFunction[i][8]) set(8, i, false); }
  for (let i = 0; i < 8; i++) { set(size - 1 - i, 8, false); set(8, size - 8 + i, false); }
  set(8, size - 8, true);

  // Zigzag data placement.
  let bi = 0;
  const total = codewords.length * 8;
  for (let right = size - 1; right >= 1; right -= 2) {
    if (right === 6) right = 5;
    for (let vert = 0; vert < size; vert++) {
      for (let j = 0; j < 2; j++) {
        const x = right - j;
        const upward = ((right + 1) & 2) === 0;
        const y = upward ? size - 1 - vert : vert;
        if (!isFunction[y][x] && bi < total) {
          modules[y][x] = ((codewords[bi >>> 3] >>> (7 - (bi & 7))) & 1) === 1;
          bi++;
        }
      }
    }
  }

  const maskFn = [
    (x, y) => (x + y) % 2 === 0,
    (x, y) => y % 2 === 0,
    (x, y) => x % 3 === 0,
    (x, y) => (x + y) % 3 === 0,
    (x, y) => (Math.floor(x / 3) + Math.floor(y / 2)) % 2 === 0,
    (x, y) => (x * y) % 2 + (x * y) % 3 === 0,
    (x, y) => ((x * y) % 2 + (x * y) % 3) % 2 === 0,
    (x, y) => ((x + y) % 2 + (x * y) % 3) % 2 === 0,
  ];
  const applyMask = (m) => { for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) if (!isFunction[y][x] && maskFn[m](x, y)) modules[y][x] = !modules[y][x]; };
  const drawFormat = (m) => {
    const dataBits = (1 << 3) | m; // level L = 01
    let rem = dataBits;
    for (let i = 0; i < 10; i++) rem = (rem << 1) ^ ((rem >>> 9) * 0x537);
    const f = ((dataBits << 10) | rem) ^ 0x5412;
    const bit = (i) => ((f >>> i) & 1) === 1;
    for (let i = 0; i <= 5; i++) set(8, i, bit(i));
    set(8, 7, bit(6)); set(8, 8, bit(7)); set(7, 8, bit(8));
    for (let i = 9; i < 15; i++) set(14 - i, 8, bit(i));
    for (let i = 0; i < 8; i++) set(size - 1 - i, 8, bit(i));
    for (let i = 8; i < 15; i++) set(8, size - 15 + i, bit(i));
    set(8, size - 8, true);
  };
  const penalty = () => {
    let score = 0;
    const runs = (get) => {
      for (let a = 0; a < size; a++) {
        let run = 1;
        for (let b = 1; b < size; b++) {
          if (get(a, b) === get(a, b - 1)) { run++; if (run === 5) score += 3; else if (run > 5) score++; } else run = 1;
        }
        for (let b = 0; b + 7 <= size; b++) {
          const p = [0, 1, 2, 3, 4, 5, 6].map((k) => get(a, b + k));
          if (p[0] && !p[1] && p[2] && p[3] && p[4] && !p[5] && p[6]) {
            const before = b >= 4 && [1, 2, 3, 4].every((k) => !get(a, b - k));
            const after = b + 10 < size + 0 && [7, 8, 9, 10].every((k) => b + k < size && !get(a, b + k));
            if (before || after) score += 40;
          }
        }
      }
    };
    runs((a, b) => modules[a][b]);
    runs((a, b) => modules[b][a]);
    let dark = 0;
    for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) {
      if (modules[y][x]) dark++;
      if (x + 1 < size && y + 1 < size && modules[y][x] === modules[y][x + 1] && modules[y][x] === modules[y + 1][x] && modules[y][x] === modules[y + 1][x + 1]) score += 3;
    }
    const k = Math.floor(Math.abs(dark * 20 - size * size * 10) / (size * size));
    return score + k * 10;
  };

  let best = 0, bestScore = Infinity;
  for (let m = 0; m < 8; m++) {
    applyMask(m); drawFormat(m);
    const s = penalty();
    if (s < bestScore) { bestScore = s; best = m; }
    applyMask(m);
  }
  applyMask(best); drawFormat(best);
  return { size, modules, version };
}

/** Render as an SVG element with a quiet zone. */
export function qrSVG(text, { quiet = 4 } = {}) {
  const { size, modules } = qrEncode(text);
  const ns = 'http://www.w3.org/2000/svg';
  const svg = document.createElementNS(ns, 'svg');
  const dim = size + quiet * 2;
  svg.setAttribute('viewBox', `0 0 ${dim} ${dim}`);
  svg.setAttribute('shape-rendering', 'crispEdges');
  svg.setAttribute('role', 'img');
  svg.setAttribute('aria-label', `QR code for ${text}`);
  const bg = document.createElementNS(ns, 'rect');
  bg.setAttribute('width', String(dim)); bg.setAttribute('height', String(dim)); bg.setAttribute('fill', '#fff');
  svg.append(bg);
  let d = '';
  for (let y = 0; y < size; y++) for (let x = 0; x < size; x++) if (modules[y][x]) d += `M${x + quiet} ${y + quiet}h1v1h-1z`;
  const path = document.createElementNS(ns, 'path');
  path.setAttribute('d', d);
  path.setAttribute('fill', '#000');
  svg.append(path);
  return svg;
}
