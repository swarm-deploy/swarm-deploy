import { createI18n } from "vue-i18n";

import { getLocale } from "../runtimeConfig";
import en from "./en.json";
import ru from "./ru.json";

const russianPlurals = new Intl.PluralRules("ru");

export const i18n = createI18n({
  legacy: false,
  locale: getLocale(),
  fallbackLocale: "en",
  messages: { en, ru },
  pluralRules: {
    ru: (count) => {
      switch (russianPlurals.select(count)) {
        case "one": return 0;
        case "few": return 1;
        default: return 2;
      }
    },
  },
});

type MessageParams = Record<string, string | number>;

export function t(key: string, params: MessageParams = {}): string {
  return i18n.global.t(key, params);
}

export function plural(key: string, count: number, params: MessageParams = {}): string {
  return i18n.global.t(key, { ...params, count: formatNumber(count) }, count);
}

export function formatNumber(value: number, options?: Intl.NumberFormatOptions): string {
  return i18n.global.n(value, options ?? {});
}

export function locale() {
  return i18n.global.locale.value;
}

// Translate known presentation values while preserving unfamiliar API values.
export function enumLabel(section: string, value: string, fallback = value): string {
  const key = `${section}.${value}`;
  return i18n.global.te(key, "en") ? t(key) : fallback;
}
