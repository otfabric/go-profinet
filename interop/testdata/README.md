# Interop fixtures

Written for go-profinet (MIT). They describe the configurations of the
reference stacks as facts (identifiers and data lengths), not copies of
their files:

- `pnet-model.json`: the p-net sample application (`pn_dev`) with the IO
  module 0x32 in slot 1 and the echo module 0x40 in slot 2, as its
  `app_gsdml.h` defines them (vendor 0x0493, device 0x0002).
- `pnet-model-api.json`: the same, with the IO module's submodule in API
  0x4E01 (19969) instead of API 0. p-net's sample plugs a module in the
  API the controller's Connect names (`app_exp_module_ind`), and its stack
  creates an API for each expected one up to `PNET_MAX_API`, which
  profinet-interop's image sets to 2 (since v0.12.0).
- `otf-device-api.json`: otfnode's built-in interop device (`defaultModel`
  in `otfnode/main.go`) with the IO module of slot 3 in API 0x4E01 and
  records on it: 0x0011, the profile record 0xB600 (16 octets) and the
  read-only profile record 0xB900 (4 octets), for `TestProfinetPyAPI`.
- The pnio-rs sample device is described by its own GSDML file, which is
  MIT licensed and kept in `gsdml/testdata/pnio-rs_sample-16real-32bool.xml`.
