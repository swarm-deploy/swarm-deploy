import { afterEach, describe, expect, it } from "vitest";

import { i18n, plural } from "./index";
import en from "./en.json";
import ru from "./ru.json";

function flatten(catalog: Record<string, Record<string, string>>): Record<string, string> {
  return Object.fromEntries(Object.entries(catalog).flatMap(([section, messages]) =>
    Object.entries(messages).map(([key, message]) => [`${section}.${key}`, message]),
  ));
}

function parameters(message: string): string[] {
  return [...new Set(Array.from(message.matchAll(/\{([a-zA-Z][\w]*)\}/g), ([, name]) => name))].sort();
}

afterEach(() => { i18n.global.locale.value = "en"; });

describe("translation catalogs", () => {
  it("shares every key and named parameter between English and Russian", () => {
    const english = flatten(en);
    const russian = flatten(ru);
    expect(Object.keys(russian).sort()).toEqual(Object.keys(english).sort());

    for (const [key, message] of Object.entries(english)) {
      const expected = parameters(message);
      for (const form of [...message.split("|"), ...russian[key].split("|")]) {
        expect(parameters(form), key).toEqual(expected);
      }
    }
  });

  it.each([
    [0, "0 сервисов"],
    [1, "1 сервис"],
    [2, "2 сервиса"],
    [5, "5 сервисов"],
    [11, "11 сервисов"],
    [12, "12 сервисов"],
    [14, "14 сервисов"],
    [21, "21 сервис"],
    [22, "22 сервиса"],
    [25, "25 сервисов"],
  ])("formats %i with the Russian plural rule", (count, expected) => {
    i18n.global.locale.value = "ru";
    expect(plural("common.serviceCount", count)).toBe(expected);
  });

  it("formats the displayed count with the selected number locale", () => {
    i18n.global.locale.value = "ru";
    expect(plural("common.serviceCount", 1001)).toBe("1\u00a0001 сервис");
  });

  it("uses the requested Russian labels on the service page", () => {
    i18n.global.locale.value = "ru";
    expect([
      i18n.global.t("service.memory"),
      i18n.global.t("service.cpu"),
      i18n.global.t("common.date"),
    ]).toEqual(["Запрос / лимит RAM", "Запрос / лимит CPU", "Дата"]);
  });
});
