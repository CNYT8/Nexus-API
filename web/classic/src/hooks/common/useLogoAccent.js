import { useEffect, useMemo, useState } from 'react';

const DEFAULT_LOGO = '/logo.png';
const logoAccentCache = new Map();
const defaultAccent = { active: false, rgb: '0, 0, 0' };

function isCustomImageLogo(src) {
  const logo = String(src || '').trim();
  return !!logo && logo !== DEFAULT_LOGO;
}

function loadImage(src) {
  return new Promise((resolve, reject) => {
    const image = new Image();
    image.crossOrigin = 'anonymous';
    image.decoding = 'async';
    image.referrerPolicy = 'no-referrer';
    image.onload = () => resolve(image);
    image.onerror = reject;
    image.src = src;
  });
}

function getSaturation(r, g, b) {
  const max = Math.max(r, g, b);
  const min = Math.min(r, g, b);
  return max === 0 ? 0 : (max - min) / max;
}

async function extractLogoAccent(src) {
  const image = await loadImage(src);
  const canvas = document.createElement('canvas');
  const size = 40;
  canvas.width = size;
  canvas.height = size;
  const context = canvas.getContext('2d', { willReadFrequently: true });
  if (!context) return null;

  context.clearRect(0, 0, size, size);
  context.drawImage(image, 0, 0, size, size);

  const pixels = context.getImageData(0, 0, size, size).data;
  const bins = new Map();

  for (let i = 0; i < pixels.length; i += 4) {
    const alpha = pixels[i + 3] / 255;
    if (alpha < 0.25) continue;

    const r = pixels[i];
    const g = pixels[i + 1];
    const b = pixels[i + 2];
    const max = Math.max(r, g, b);
    const min = Math.min(r, g, b);
    const saturation = getSaturation(r, g, b);

    if (max < 24 || min > 244 || saturation < 0.08) continue;

    const bucket = `${Math.round(r / 24)}:${Math.round(g / 24)}:${Math.round(b / 24)}`;
    const colorStrength = 0.35 + saturation * 1.45;
    const weight = alpha * colorStrength * Math.sqrt(max / 255);
    const current = bins.get(bucket) || { count: 0, r: 0, g: 0, b: 0 };
    current.count += weight;
    current.r += r * weight;
    current.g += g * weight;
    current.b += b * weight;
    bins.set(bucket, current);
  }

  const dominant = [...bins.values()].sort((a, b) => b.count - a.count)[0];
  if (!dominant || dominant.count <= 0) return null;

  const r = Math.round(dominant.r / dominant.count);
  const g = Math.round(dominant.g / dominant.count);
  const b = Math.round(dominant.b / dominant.count);
  return `${r}, ${g}, ${b}`;
}

export function useLogoAccent(logo) {
  const normalizedLogo = useMemo(() => String(logo || '').trim(), [logo]);
  const [accentRgb, setAccentRgb] = useState(() =>
    isCustomImageLogo(normalizedLogo)
      ? logoAccentCache.get(normalizedLogo) || null
      : null,
  );

  useEffect(() => {
    let cancelled = false;
    if (!isCustomImageLogo(normalizedLogo)) {
      setAccentRgb(null);
      return;
    }

    if (logoAccentCache.has(normalizedLogo)) {
      setAccentRgb(logoAccentCache.get(normalizedLogo) || null);
      return;
    }

    extractLogoAccent(normalizedLogo)
      .then((rgb) => {
        logoAccentCache.set(normalizedLogo, rgb);
        if (!cancelled) setAccentRgb(rgb);
      })
      .catch(() => {
        logoAccentCache.set(normalizedLogo, null);
        if (!cancelled) setAccentRgb(null);
      });

    return () => {
      cancelled = true;
    };
  }, [normalizedLogo]);

  return accentRgb ? { active: true, rgb: accentRgb } : defaultAccent;
}
