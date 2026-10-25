'use strict';

// โหลดก่อนไฟล์ web/i18n/<รหัส>.js แล้ว app.js รวมเข้า I18N
var LANG_ORDER = ['th', 'en', 'zh', 'es', 'hi', 'ar', 'fr', 'pt', 'ru', 'ja', 'de', 'ko', 'bn', 'id', 'lo', 'km'];
var LANG_LABELS = {
  th: 'ไทย',
  en: 'English',
  zh: '中文',
  es: 'Español',
  hi: 'हिन्दी',
  ar: 'العربية',
  fr: 'Français',
  pt: 'Português',
  ru: 'Русский',
  ja: '日本語',
  de: 'Deutsch',
  ko: '한국어',
  bn: 'বাংলা',
  id: 'Bahasa Indonesia',
  lo: 'ລາວ',
  km: 'ខ្មែរ',
};
var I18N_MORE = I18N_MORE || {};
