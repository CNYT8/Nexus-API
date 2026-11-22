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
import i18n from 'i18next'
import LanguageDetector from 'i18next-browser-languagedetector'
import { initReactI18next } from 'react-i18next'
import en from './locales/en.json'
import fr from './locales/fr.json'
import ja from './locales/ja.json'
import ru from './locales/ru.json'
import vi from './locales/vi.json'
import zh from './locales/zh.json'
import zhTWOverrides from './locales/zh-TW.json'
import classicZhTW from '../../../classic/src/i18n/locales/zh-TW.json'
import { INTERFACE_LANGUAGE_OPTIONS, normalizeInterfaceLanguage } from './languages'

// Preserve Nexus's complete key set. Prefer reviewed Traditional translations
// already shipped by Classic; the remaining source text uses the local upstream
// terminology map below. Explicit translations in zh-TW.json take precedence.
const simplified = '万与专业丢两严个临为举么义乐习买于产亲仅从仓仪们价众优会传伪体侧储兑关内册写冲决况冻减凑几凭击则刚创删别剥剧务动励势区医华协单卖占厂历压参双发变叠号后吗启员响团园围国图圆场坏块坚声处备复够头夹奖学宝实审宽对导将尝尽层属币帐带帧帮并广库应废开异弃张弹强归当录彻征径态总恶惩愿户执扩扫护报担拟拥拦择损换据携数断无旧时显晓暂术机杂权杠条来极构标栏样档梦检槛横欢欧毁毕汇沟没泄洁浅测浏温溃滚滞满滤滥灵点热状独献环现电画畅疗监盖盗盘着码础确离种积称税稳笔筛签简类紧约级纯纹线练组细织终绍经绑结绘给络绝统继绪续维综缀缓编缩网罚联脑脚脸腾节范荐获营虑补装见观规视览觉触计订认讨让训议讯记许论设访证评识诉词译试话询该详语误说请诺读调谨谱负贡财责败账货质购贴费资赔赖赚赠趋跃践踪车转轮软轻载较辅辑输辞边达过运还这进违连迟适选递逻邮采释鉴针钟钥钮钱钻铺链销锁错键镜长门闭问间阅阈队阶际陆险随隐静页顶项顺须顾预领颈频题颜额风飞饼馈驱驻验骤黄齐'
const traditional = '萬與專業丟兩嚴個臨為舉麼義樂習買於產親僅從倉儀們價眾優會傳偽體側儲兌關內冊寫衝決況凍減湊幾憑擊則剛創刪別剝劇務動勵勢區醫華協單賣佔廠歷壓參雙發變疊號後嗎啟員響團園圍國圖圓場壞塊堅聲處備復夠頭夾獎學寶實審寬對導將嘗盡層屬幣帳帶幀幫並廣庫應廢開異棄張彈強歸當錄徹徵徑態總惡懲願戶執擴掃護報擔擬擁攔擇損換據攜數斷無舊時顯曉暫術機雜權槓條來極構標欄樣檔夢檢檻橫歡歐毀畢匯溝沒洩潔淺測瀏溫潰滾滯滿濾濫靈點熱狀獨獻環現電畫暢療監蓋盜盤著碼礎確離種積稱稅穩筆篩簽簡類緊約級純紋線練組細織終紹經綁結繪給絡絕統繼緒續維綜綴緩編縮網罰聯腦腳臉騰節範薦獲營慮補裝見觀規視覽覺觸計訂認討讓訓議訊記許論設訪證評識訴詞譯試話詢該詳語誤說請諾讀調謹譜負貢財責敗賬貨質購貼費資賠賴賺贈趨躍踐蹤車轉輪軟輕載較輔輯輸辭邊達過運還這進違連遲適選遞邏郵採釋鑒針鐘鑰鈕錢鑽鋪鏈銷鎖錯鍵鏡長門閉問間閱閾隊階際陸險隨隱靜頁頂項順須顧預領頸頻題顏額風飛餅饋驅駐驗驟黃齊'
const characters = new Map([...simplified].map((character, index) => [character, traditional[index]]))
// Terms from the local new-api zh/zh-TW resources, with phrases before characters.
const terminology: Record<string, string> = {
  '用户': '使用者', '客户端': '用戶端', '服务器': '伺服器',
  '默认': '預設', '配置': '設定', '设置': '設定', '字段': '欄位',
  '添加': '新增', '插件': '外掛', '访问': '存取', '信息': '資訊',
  '文件': '檔案', '调用': '呼叫', '表达式': '運算式', '对象': '物件',
  '网关': '閘道', '令牌': '權杖', '列表': '清單', '查看': '檢視',
  '会话': '工作階段', '链接': '連結', '高级': '進階', '数组': '陣列',
  '视频': '影片', '缓存': '快取', '数据': '資料', '集成': '整合',
  '内存': '記憶體', '声明': '宣告', '接口': '介面', '激活': '啟用',
  '审计': '稽核', '端口': '連接埠', '账户': '帳號', '元数据': '中繼資料',
  '套餐': '訂閱方案', '替换': '取代', '设备': '裝置', '匹配': '比對',
  '通配符': '萬用字元', '项目': '專案', '自定义': '自訂',
  '返回': '回傳', '响应': '回應', '请求头': '請求標頭', '加载': '載入',
  '保存': '儲存', '删除': '刪除', '重置': '重設', '刷新': '重新整理',
  '登录': '登入', '充值': '儲值', '密钥': '金鑰', '消息': '訊息',
  '网络': '網路', '支持': '支援', '启用': '啟用', '禁用': '停用',
  '创建': '建立', '复制': '複製', '重复': '重複', '恢复': '恢復',
  '后台': '後台', '汇总': '彙總', '计划': '計畫', '里面': '裡面',
}
const phrasePattern = new RegExp(Object.keys(terminology).sort((a, b) => b.length - a.length).join('|'), 'g')
function toTraditional(text: string): string {
  // Technical literals and interpolation names must remain byte-for-byte intact.
  return text.split(/(\{\{[^}]*\}\}|`[^`]*`|https?:\/\/[^\s]+|\$\([^)]*\))/g)
    .map((part, index) => index % 2 ? part : [...part.replace(phrasePattern, (word) => terminology[word])]
      .map((character) => characters.get(character) || character).join(''))
    .join('')
}
const classicTranslations: Record<string, string> = classicZhTW.translation
const traditionalOverrides: Record<string, string> = zhTWOverrides.translation
const zhTW = {
  translation: Object.fromEntries(Object.entries(zh.translation).map(([key, source]) => [
    key,
    traditionalOverrides[key] || toTraditional(classicTranslations[source] || source),
  ])),
}
Object.assign(zhTW.translation, zhTWOverrides.translation)

export const resources = {
  en,
  zh,
  'zh-TW': zhTW,
  fr,
  ru,
  ja,
  vi,
} as const

i18n
  .use(LanguageDetector)
  .use(initReactI18next)
  .init({
    resources,
    fallbackLng: 'en',
    supportedLngs: INTERFACE_LANGUAGE_OPTIONS.map(({ code }) => code),
    load: 'currentOnly',
    nsSeparator: false, // Allow literal colons in keys (e.g., URLs, labels)
    debug: import.meta.env.DEV,
    interpolation: {
      escapeValue: false, // not needed for react as it escapes by default
    },
    detection: {
      order: ['localStorage', 'navigator'],
      caches: ['localStorage'],
      convertDetectedLanguage: normalizeInterfaceLanguage,
    },
  })

export default i18n
