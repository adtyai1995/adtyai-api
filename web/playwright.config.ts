/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { defineConfig, devices } from '@playwright/test'

const port = 4173
const isolatedBaseURL = process.env.ADTY_BROWSER_BASE_URL
const baseURL = isolatedBaseURL || `http://127.0.0.1:${port}`
const chromiumPath = isolatedBaseURL && process.env.ADTY_CHROMIUM_PATH
const seoScenario = process.env.ADTY_BROWSER_SCENARIO === 'ftr-seo'
const devCommand =
  process.platform === 'win32' ? 'node_modules\\.bin\\rsbuild.exe dev' : 'bun run dev'

if (isolatedBaseURL && !['127.0.0.1', 'localhost', '[::1]'].includes(new URL(isolatedBaseURL).hostname)) {
  throw new Error('The public FTR requires a local, isolated Go application')
}

export default defineConfig({
  testDir: './e2e',
  fullyParallel: false,
  workers: 1,
  retries: 0,
  forbidOnly: true,
  // The isolated Redis rate limit can return a Retry-After of up to 180s.
  timeout: isolatedBaseURL ? 260_000 : 60_000,
  expect: { timeout: 20_000 },
  ...(isolatedBaseURL && {
    testMatch: '**/adty-ftr-public.spec.ts',
    grep: seoScenario
      ? /\bADTY-01-09\b/
      : /\bADTY-(?:01-(?:01|04|10)|22-(?:0[1-9]|10))\b/,
  }),
  reporter: [
    ['list'],
    [
      'json',
      {
        outputFile: isolatedBaseURL
          ? 'e2e-results/adty-ftr-public-real.json'
          : 'e2e-results/adty-ftr-public.json',
      },
    ],
  ],
  use: {
    baseURL,
    trace: 'off',
    video: 'off',
    screenshot: 'only-on-failure',
  },
  webServer: isolatedBaseURL
    ? undefined
    : {
        command: `${devCommand} --port ${port} --host 127.0.0.1`,
        url: baseURL,
        reuseExistingServer: false,
        timeout: 180_000,
      },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        ...(chromiumPath && { launchOptions: { executablePath: chromiumPath } }),
      },
    },
  ],
})
