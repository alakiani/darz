// The panels' Mantine theme. The Coordinator's CSP refuses <style> elements,
// so Mantine's runtime CSS variables are off (see main.tsx) and this theme's
// variables live in theme.css instead; theme.test.tsx keeps the two in step.
import { createTheme, Paper, Table } from "@mantine/core";

export const theme = createTheme({
  primaryColor: "indigo",
  primaryShade: { light: 6, dark: 5 },
  defaultRadius: "md",
  fontFamily: 'system-ui, -apple-system, "Segoe UI", Roboto, Tahoma, "Noto Sans Arabic", sans-serif',
  headings: { fontWeight: "700" },
  components: {
    Paper: Paper.extend({ defaultProps: { withBorder: true, radius: "lg" } }),
    Table: Table.extend({ defaultProps: { verticalSpacing: "xs", highlightOnHover: true } }),
  },
});
