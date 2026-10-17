# Media security fixtures

- `valid.png` / `valid.jpg`: supported 2×2 images.
- `exif-gps.jpg`: valid JPEG with an APP1 payload containing recognizable GPS metadata.
- `polyglot.png`: valid PNG followed by shell text; strict intake must reject trailing data.
- `dimension-bomb.png`: PNG header advertising 50,000×50,000 pixels; rejected before full decode.
- `invalid.png`: PNG signature followed by malformed data.

The fixtures are deliberately tiny so bounded corpus/fuzz smoke tests are suitable for CI.
