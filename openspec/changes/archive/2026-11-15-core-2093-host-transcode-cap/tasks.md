## 1. Derivation

- [x] 1.1 `ffmpeg.DefaultTranscodeCap` derives the cap from the selected accelerator and usable cores
- [x] 1.2 Unit tests: software clamp at both ends, each hardware backend limit, the hardware floor

## 2. Startup

- [x] 2.1 `app.New` uses the selected accelerator's derived cap as the automatic cap and logs it
- [x] 2.2 Existing precedence (env/flag, stored, automatic) stays in `graph.ResolveTranscodeCap`
