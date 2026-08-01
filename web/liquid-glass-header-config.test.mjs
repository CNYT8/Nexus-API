import assert from 'node:assert/strict';
import { getLiquidGlassHeaderProps } from './liquid-glass-header-config.js';

const lightProps = getLiquidGlassHeaderProps({ overLight: true });
const darkProps = getLiquidGlassHeaderProps({ overLight: false });
const roundedProps = getLiquidGlassHeaderProps({ cornerRadius: 16 });

assert.equal(lightProps.mode, 'standard');
assert.equal(lightProps.cornerRadius, 0);
assert.equal(roundedProps.cornerRadius, 16);
assert.equal(lightProps.elasticity, 0);
assert.equal(lightProps.globalMousePos.x, 0);
assert.equal(lightProps.mouseOffset.y, 0);
assert.equal(lightProps.overLight, true);
assert.equal(darkProps.overLight, false);
assert.ok(lightProps.blurAmount < darkProps.blurAmount);
assert.equal(lightProps.displacementScale, 14);
assert.equal(lightProps.aberrationIntensity, 0.35);

console.log('liquid glass header configuration: ok');
