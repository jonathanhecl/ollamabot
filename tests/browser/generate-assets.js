const fs = require('fs');
const path = require('path');

const assetsDir = path.join(__dirname, 'assets');
if (!fs.existsSync(assetsDir)) {
  fs.mkdirSync(assetsDir, { recursive: true });
}

// 1x1 pixel transparent/black PNG hexadecimal bytes
const pngHex = '89504e470d0a1a0a0000000d49484452000000010000000108060000001f15c4890000000d49444154789c6360606060000000050001a5f2dd450000000049454e44ae426082';
const pngBuffer = Buffer.from(pngHex, 'hex');

const targetPath = path.join(assetsDir, 'test_image.png');
fs.writeFileSync(targetPath, pngBuffer);
console.log('Generated mock image asset at:', targetPath);
