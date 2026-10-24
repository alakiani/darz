import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { DirectionProvider, MantineProvider } from "@mantine/core";
import "@mantine/core/styles.css";
import "@fontsource-variable/vazirmatn/wght.css";
import { App } from "./App";
import { theme } from "./ui/theme";
import "./ui/theme.css";
import "./styles.css";

// No <style> elements: the CSP allows only 'self' stylesheets, so the
// theme's variables come from theme.css and responsive style props, which
// inject <style>, stay unused. The direction follows <html dir>, which the
// admin panel flips to ltr.
createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <DirectionProvider initialDirection="rtl">
      <MantineProvider theme={theme} defaultColorScheme="auto" withCssVariables={false} withGlobalClasses={false}>
        <App />
      </MantineProvider>
    </DirectionProvider>
  </StrictMode>,
);
