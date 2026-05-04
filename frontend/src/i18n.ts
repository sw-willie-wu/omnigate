import { createI18n } from 'vue-i18n';
import zhTW from './locales/zh-TW.json';
import zhCN from './locales/zh-CN.json';
import en from './locales/en.json';

export const i18n = createI18n({
  legacy: false,
  locale: 'zh-TW',
  fallbackLocale: 'en',
  messages: { 'zh-TW': zhTW, 'zh-CN': zhCN, en },
});

export function setLang(lang: 'zh-TW' | 'zh-CN' | 'en') {
  i18n.global.locale.value = lang;
  // BCP47-ish HTML lang attribute hint for the browser/font selection
  const htmlLang =
    lang === 'en' ? 'en' :
    lang === 'zh-CN' ? 'zh-Hans' :
    'zh-Hant';
  document.documentElement.setAttribute('lang', htmlLang);
}
