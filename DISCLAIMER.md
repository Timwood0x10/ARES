# Disclaimer

## Software Status

ARES is provided "as is" and "as available", without warranty of any kind,
express or implied. The software is under active development and should be
considered **beta quality**.

## No Financial Advice

ARES includes a quantitative trading module (`internal/ares_quant/`) intended
**for research and educational purposes only**. It is **not** financial advice,
investment advice, trading advice, or any other sort of advice.

You are solely responsible for any decisions you make based on information
obtained from this software. Always conduct your own due diligence and consult
with a qualified financial advisor before making investment decisions.

Past performance is not indicative of future results. Trading involves
substantial risk of loss and is not suitable for every investor.

## No Warranty

Under the terms of the Apache License, Version 2.0:

> Unless required by applicable law or agreed to in writing, software
> distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
> WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.

The authors and copyright holders of ARES assume no liability for any damages
arising from the use or inability to use this software.

## Experimental Components

Several modules are explicitly marked as experimental:

- **Quant Trading Module** (`internal/ares_quant/`) — 9,768 lines of
  domain-specific trading code; see
  [the quant deep dive](docs/articles/en/00-quant-trading.md) for the honest
  assessment.

These modules may be refactored, reorganized, or extracted without notice.
