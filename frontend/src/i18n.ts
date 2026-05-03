import { createI18n } from 'vue-i18n';
import zhTW from './locales/zh-TW.json';
import en from './locales/en.json';

export const i18n = createI18n({
  legacy: false,
  locale: 'zh-TW',
  fallbackLocale: 'en',
  messages: { 'zh-TW': zhTW, en },
});

export function setLang(lang: 'zh-TW' | 'en') {
  i18n.global.locale.value = lang;
  document.documentElement.setAttribute('lang', lang === 'en' ? 'en' : 'zh-Hant');
}
