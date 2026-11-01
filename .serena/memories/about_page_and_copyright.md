# About page and the copyright notice

`/about` (`ui/src/pages/system/AboutPage.tsx`) states who owns Hermod and which
build is running. Every role can reach it: the last row of the Settings list
(`settingsRows` in `ui/src/components/layout/nav.ts`, "About Hermod") and an
item in the account menu (`Layout.tsx`). `sectionOf('/about')` is `settings`.

The notice repeats the LICENSE file word for word: "© 2025 Gembit Soultan
Shirazi. All rights reserved.", the Hermod Proprietary License, and a one-line
summary of its "no license is granted" clause. If LICENSE changes (year range,
wording), change `COPYRIGHT_NOTICE` and the paragraph under it to match;
`ui/src/__tests__/aboutPage.test.tsx` pins the exact text.

The version comes from `GET /api/version` (public, no session needed), cut to
its semver head the same way the account menu does; the full build string is
shown under it when it differs.
