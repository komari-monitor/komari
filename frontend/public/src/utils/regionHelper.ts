const REGION_FLAG_REGEX = /[\u{1F1E0}-\u{1F1FF}]{2}/gu

// 地区emoji到名称的映射
export const emojiToRegionMap: Record<string, { en: string, zh: string, code: string, aliases: string[] }> = {
  '🇭🇰': {
    en: 'Hong Kong',
    zh: '香港',
    code: 'HK',
    aliases: ['hk', 'hongkong', 'hong kong', '香港', 'HK'],
  },
  '🇨🇳': {
    en: 'China',
    zh: '中国',
    code: 'CN',
    aliases: ['cn', 'china', '中国', '中华人民共和国', 'prc', 'CN'],
  },
  '🇺🇸': {
    en: 'United States',
    zh: '美国',
    code: 'US',
    aliases: ['us', 'usa', 'united states', 'america', '美国', '美利坚', 'US', 'USA'],
  },
  '🇯🇵': {
    en: 'Japan',
    zh: '日本',
    code: 'JP',
    aliases: ['jp', 'japan', '日本', 'JP'],
  },
  '🇰🇷': {
    en: 'South Korea',
    zh: '韩国',
    code: 'KR',
    aliases: ['kr', 'korea', 'south korea', '韩国', '南韩', 'KR'],
  },
  '🇸🇬': {
    en: 'Singapore',
    zh: '新加坡',
    code: 'SG',
    aliases: ['sg', 'singapore', '新加坡', 'SG'],
  },
  '🇹🇼': {
    en: 'Taiwan',
    zh: '台湾',
    code: 'TW',
    aliases: ['tw', 'taiwan', '台湾', '台灣', 'TW'],
  },
  '🇬🇧': {
    en: 'United Kingdom',
    zh: '英国',
    code: 'GB',
    aliases: ['gb', 'uk', 'united kingdom', 'britain', '英国', '英國', 'GB', 'UK'],
  },
  '🇩🇪': {
    en: 'Germany',
    zh: '德国',
    code: 'DE',
    aliases: ['de', 'germany', 'deutschland', '德国', '德國', 'DE'],
  },
  '🇫🇷': {
    en: 'France',
    zh: '法国',
    code: 'FR',
    aliases: ['fr', 'france', '法国', '法國', 'FR'],
  },
  '🇨🇦': {
    en: 'Canada',
    zh: '加拿大',
    code: 'CA',
    aliases: ['ca', 'canada', '加拿大', 'CA'],
  },
  '🇦🇺': {
    en: 'Australia',
    zh: '澳大利亚',
    code: 'AU',
    aliases: ['au', 'australia', '澳大利亚', '澳洲', 'AU'],
  },
  '🇷🇺': {
    en: 'Russia',
    zh: '俄罗斯',
    code: 'RU',
    aliases: ['ru', 'russia', '俄罗斯', '俄國', 'RU'],
  },
  '🇮🇳': {
    en: 'India',
    zh: '印度',
    code: 'IN',
    aliases: ['in', 'india', '印度', 'IN'],
  },
  '🇧🇷': {
    en: 'Brazil',
    zh: '巴西',
    code: 'BR',
    aliases: ['br', 'brazil', '巴西', 'BR'],
  },
  '🇳🇱': {
    en: 'Netherlands',
    zh: '荷兰',
    code: 'NL',
    aliases: ['nl', 'netherlands', 'holland', '荷兰', '荷蘭', 'NL'],
  },
  '🇮🇹': {
    en: 'Italy',
    zh: '意大利',
    code: 'IT',
    aliases: ['it', 'italy', '意大利', 'IT'],
  },
  '🇪🇸': {
    en: 'Spain',
    zh: '西班牙',
    code: 'ES',
    aliases: ['es', 'spain', '西班牙', 'ES'],
  },
  '🇸🇪': {
    en: 'Sweden',
    zh: '瑞典',
    code: 'SE',
    aliases: ['se', 'sweden', '瑞典', 'SE'],
  },
  '🇳🇴': {
    en: 'Norway',
    zh: '挪威',
    code: 'NO',
    aliases: ['no', 'norway', '挪威', 'NO'],
  },
  '🇫🇮': {
    en: 'Finland',
    zh: '芬兰',
    code: 'FI',
    aliases: ['fi', 'finland', '芬兰', '芬蘭', 'FI'],
  },
  '🇨🇭': {
    en: 'Switzerland',
    zh: '瑞士',
    code: 'CH',
    aliases: ['ch', 'switzerland', '瑞士', 'CH'],
  },
  '🇦🇹': {
    en: 'Austria',
    zh: '奥地利',
    code: 'AT',
    aliases: ['at', 'austria', '奥地利', '奧地利', 'AT'],
  },
  '🇧🇪': {
    en: 'Belgium',
    zh: '比利时',
    code: 'BE',
    aliases: ['be', 'belgium', '比利时', '比利時', 'BE'],
  },
  '🇵🇹': {
    en: 'Portugal',
    zh: '葡萄牙',
    code: 'PT',
    aliases: ['pt', 'portugal', '葡萄牙', 'PT'],
  },
  '🇬🇷': {
    en: 'Greece',
    zh: '希腊',
    code: 'GR',
    aliases: ['gr', 'greece', '希腊', '希臘', 'GR'],
  },
  '🇹🇷': {
    en: 'Turkey',
    zh: '土耳其',
    code: 'TR',
    aliases: ['tr', 'turkey', '土耳其', 'TR'],
  },
  '🇵🇱': {
    en: 'Poland',
    zh: '波兰',
    code: 'PL',
    aliases: ['pl', 'poland', '波兰', '波蘭', 'PL'],
  },
  '🇨🇿': {
    en: 'Czech Republic',
    zh: '捷克',
    code: 'CZ',
    aliases: ['cz', 'czech', 'czech republic', '捷克', 'CZ'],
  },
  '🇭🇺': {
    en: 'Hungary',
    zh: '匈牙利',
    code: 'HU',
    aliases: ['hu', 'hungary', '匈牙利', 'HU'],
  },
  '🇷🇴': {
    en: 'Romania',
    zh: '罗马尼亚',
    code: 'RO',
    aliases: ['ro', 'romania', '罗马尼亚', '羅馬尼亞', 'RO'],
  },
  '🇧🇬': {
    en: 'Bulgaria',
    zh: '保加利亚',
    code: 'BG',
    aliases: ['bg', 'bulgaria', '保加利亚', '保加利亞', 'BG'],
  },
  '🇭🇷': {
    en: 'Croatia',
    zh: '克罗地亚',
    code: 'HR',
    aliases: ['hr', 'croatia', '克罗地亚', '克羅地亞', 'HR'],
  },
  '🇸🇮': {
    en: 'Slovenia',
    zh: '斯洛文尼亚',
    code: 'SI',
    aliases: ['si', 'slovenia', '斯洛文尼亚', '斯洛文尼亞', 'SI'],
  },
  '🇸🇰': {
    en: 'Slovakia',
    zh: '斯洛伐克',
    code: 'SK',
    aliases: ['sk', 'slovakia', '斯洛伐克', 'SK'],
  },
  '🇱🇻': {
    en: 'Latvia',
    zh: '拉脱维亚',
    code: 'LV',
    aliases: ['lv', 'latvia', '拉脱维亚', '拉脫維亞', 'LV'],
  },
  '🇱🇹': {
    en: 'Lithuania',
    zh: '立陶宛',
    code: 'LT',
    aliases: ['lt', 'lithuania', '立陶宛', 'LT'],
  },
  '🇪🇪': {
    en: 'Estonia',
    zh: '爱沙尼亚',
    code: 'EE',
    aliases: ['ee', 'estonia', '爱沙尼亚', '愛沙尼亞', 'EE'],
  },
  '🇲🇽': {
    en: 'Mexico',
    zh: '墨西哥',
    code: 'MX',
    aliases: ['mx', 'mexico', '墨西哥', 'MX'],
  },
  '🇦🇷': {
    en: 'Argentina',
    zh: '阿根廷',
    code: 'AR',
    aliases: ['ar', 'argentina', '阿根廷', 'AR'],
  },
  '🇨🇱': {
    en: 'Chile',
    zh: '智利',
    code: 'CL',
    aliases: ['cl', 'chile', '智利', 'CL'],
  },
  '🇨🇴': {
    en: 'Colombia',
    zh: '哥伦比亚',
    code: 'CO',
    aliases: ['co', 'colombia', '哥伦比亚', '哥倫比亞', 'CO'],
  },
  '🇵🇪': {
    en: 'Peru',
    zh: '秘鲁',
    code: 'PE',
    aliases: ['pe', 'peru', '秘鲁', '秘魯', 'PE'],
  },
  '🇻🇪': {
    en: 'Venezuela',
    zh: '委内瑞拉',
    code: 'VE',
    aliases: ['ve', 'venezuela', '委内瑞拉', '委內瑞拉', 'VE'],
  },
  '🇺🇾': {
    en: 'Uruguay',
    zh: '乌拉圭',
    code: 'UY',
    aliases: ['uy', 'uruguay', '乌拉圭', '烏拉圭', 'UY'],
  },
  '🇪🇨': {
    en: 'Ecuador',
    zh: '厄瓜多尔',
    code: 'EC',
    aliases: ['ec', 'ecuador', '厄瓜多尔', '厄瓜多爾', 'EC'],
  },
  '🇧🇴': {
    en: 'Bolivia',
    zh: '玻利维亚',
    code: 'BO',
    aliases: ['bo', 'bolivia', '玻利维亚', '玻利維亞', 'BO'],
  },
  '🇵🇾': {
    en: 'Paraguay',
    zh: '巴拉圭',
    code: 'PY',
    aliases: ['py', 'paraguay', '巴拉圭', 'PY'],
  },
  '🇬🇾': {
    en: 'Guyana',
    zh: '圭亚那',
    code: 'GY',
    aliases: ['gy', 'guyana', '圭亚那', '圭亞那', 'GY'],
  },
  '🇸🇷': {
    en: 'Suriname',
    zh: '苏里南',
    code: 'SR',
    aliases: ['sr', 'suriname', '苏里南', '蘇里南', 'SR'],
  },
  '🇫🇰': {
    en: 'Falkland Islands',
    zh: '福克兰群岛',
    code: 'FK',
    aliases: ['fk', 'falkland', '福克兰', '福克蘭', 'FK'],
  },
  '🇬🇫': {
    en: 'French Guiana',
    zh: '法属圭亚那',
    code: 'GF',
    aliases: ['gf', 'french guiana', '法属圭亚那', '法屬圭亞那', 'GF'],
  },
  '🇵🇦': {
    en: 'Panama',
    zh: '巴拿马',
    code: 'PA',
    aliases: ['pa', 'panama', '巴拿马', '巴拿馬', 'PA'],
  },
  '🇨🇷': {
    en: 'Costa Rica',
    zh: '哥斯达黎加',
    code: 'CR',
    aliases: ['cr', 'costa rica', '哥斯达黎加', '哥斯達黎加', 'CR'],
  },
  '🇳🇮': {
    en: 'Nicaragua',
    zh: '尼加拉瓜',
    code: 'NI',
    aliases: ['ni', 'nicaragua', '尼加拉瓜', 'NI'],
  },
  '🇭🇳': {
    en: 'Honduras',
    zh: '洪都拉斯',
    code: 'HN',
    aliases: ['hn', 'honduras', '洪都拉斯', 'HN'],
  },
  '🇬🇹': {
    en: 'Guatemala',
    zh: '危地马拉',
    code: 'GT',
    aliases: ['gt', 'guatemala', '危地马拉', '危地馬拉', 'GT'],
  },
  '🇧🇿': {
    en: 'Belize',
    zh: '伯利兹',
    code: 'BZ',
    aliases: ['bz', 'belize', '伯利兹', '伯利茲', 'BZ'],
  },
  '🇸🇻': {
    en: 'El Salvador',
    zh: '萨尔瓦多',
    code: 'SV',
    aliases: ['sv', 'el salvador', '萨尔瓦多', '薩爾瓦多', 'SV'],
  },
  '🇯🇲': {
    en: 'Jamaica',
    zh: '牙买加',
    code: 'JM',
    aliases: ['jm', 'jamaica', '牙买加', '牙買加', 'JM'],
  },
  '🇨🇺': {
    en: 'Cuba',
    zh: '古巴',
    code: 'CU',
    aliases: ['cu', 'cuba', '古巴', 'CU'],
  },
  '🇩🇴': {
    en: 'Dominican Republic',
    zh: '多明尼加',
    code: 'DO',
    aliases: ['do', 'dominican', '多明尼加', 'DO'],
  },
  '🇭🇹': {
    en: 'Haiti',
    zh: '海地',
    code: 'HT',
    aliases: ['ht', 'haiti', '海地', 'HT'],
  },
  '🇧🇸': {
    en: 'Bahamas',
    zh: '巴哈马',
    code: 'BS',
    aliases: ['bs', 'bahamas', '巴哈马', '巴哈馬', 'BS'],
  },
  '🇧🇧': {
    en: 'Barbados',
    zh: '巴巴多斯',
    code: 'BB',
    aliases: ['bb', 'barbados', '巴巴多斯', 'BB'],
  },
  '🇹🇹': {
    en: 'Trinidad and Tobago',
    zh: '特立尼达和多巴哥',
    code: 'TT',
    aliases: ['tt', 'trinidad', '特立尼达', '特立尼達', 'TT'],
  },
  '🇵🇭': {
    en: 'Philippines',
    zh: '菲律宾',
    code: 'PH',
    aliases: ['ph', 'philippines', '菲律宾', '菲律賓', 'PH'],
  },
  '🇹🇭': {
    en: 'Thailand',
    zh: '泰国',
    code: 'TH',
    aliases: ['th', 'thailand', '泰国', '泰國', 'TH'],
  },
  '🇻🇳': {
    en: 'Vietnam',
    zh: '越南',
    code: 'VN',
    aliases: ['vn', 'vietnam', '越南', 'VN'],
  },
  '🇲🇾': {
    en: 'Malaysia',
    zh: '马来西亚',
    code: 'MY',
    aliases: ['my', 'malaysia', '马来西亚', '馬來西亞', 'MY'],
  },
  '🇮🇩': {
    en: 'Indonesia',
    zh: '印度尼西亚',
    code: 'ID',
    aliases: ['id', 'indonesia', '印度尼西亚', '印尼', 'ID'],
  },
  '🇱🇦': {
    en: 'Laos',
    zh: '老挝',
    code: 'LA',
    aliases: ['la', 'laos', '老挝', '老撾', 'LA'],
  },
  '🇰🇭': {
    en: 'Cambodia',
    zh: '柬埔寨',
    code: 'KH',
    aliases: ['kh', 'cambodia', '柬埔寨', 'KH'],
  },
  '🇲🇲': {
    en: 'Myanmar',
    zh: '缅甸',
    code: 'MM',
    aliases: ['mm', 'myanmar', 'burma', '缅甸', '緬甸', 'MM'],
  },
  '🇧🇳': {
    en: 'Brunei',
    zh: '文莱',
    code: 'BN',
    aliases: ['bn', 'brunei', '文莱', '汶萊', 'BN'],
  },
  '🇪🇬': {
    en: 'Egypt',
    zh: '埃及',
    code: 'EG',
    aliases: ['eg', 'egypt', '埃及', 'EG'],
  },
  '🇿🇦': {
    en: 'South Africa',
    zh: '南非',
    code: 'ZA',
    aliases: ['za', 'south africa', '南非', 'ZA'],
  },
  '🇳🇬': {
    en: 'Nigeria',
    zh: '尼日利亚',
    code: 'NG',
    aliases: ['ng', 'nigeria', '尼日利亚', '尼日利亞', 'NG'],
  },
  '🇰🇪': {
    en: 'Kenya',
    zh: '肯尼亚',
    code: 'KE',
    aliases: ['ke', 'kenya', '肯尼亚', '肯亞', 'KE'],
  },
  '🇪🇹': {
    en: 'Ethiopia',
    zh: '埃塞俄比亚',
    code: 'ET',
    aliases: ['et', 'ethiopia', '埃塞俄比亚', '埃塞俄比亞', 'ET'],
  },
  '🇬🇭': {
    en: 'Ghana',
    zh: '加纳',
    code: 'GH',
    aliases: ['gh', 'ghana', '加纳', '迦納', 'GH'],
  },
  '🇺🇬': {
    en: 'Uganda',
    zh: '乌干达',
    code: 'UG',
    aliases: ['ug', 'uganda', '乌干达', '烏干達', 'UG'],
  },
  '🇹🇿': {
    en: 'Tanzania',
    zh: '坦桑尼亚',
    code: 'TZ',
    aliases: ['tz', 'tanzania', '坦桑尼亚', '坦尚尼亞', 'TZ'],
  },
  '🇷🇼': {
    en: 'Rwanda',
    zh: '卢旺达',
    code: 'RW',
    aliases: ['rw', 'rwanda', '卢旺达', '盧旺達', 'RW'],
  },
  '🇿🇼': {
    en: 'Zimbabwe',
    zh: '津巴布韦',
    code: 'ZW',
    aliases: ['zw', 'zimbabwe', '津巴布韦', '辛巴威', 'ZW'],
  },
  '🇿🇲': {
    en: 'Zambia',
    zh: '赞比亚',
    code: 'ZM',
    aliases: ['zm', 'zambia', '赞比亚', '尚比亞', 'ZM'],
  },
  '🇧🇼': {
    en: 'Botswana',
    zh: '博茨瓦纳',
    code: 'BW',
    aliases: ['bw', 'botswana', '博茨瓦纳', '波札那', 'BW'],
  },
  '🇳🇦': {
    en: 'Namibia',
    zh: '纳米比亚',
    code: 'NA',
    aliases: ['na', 'namibia', '纳米比亚', '納米比亞', 'NA'],
  },
  '🇲🇦': {
    en: 'Morocco',
    zh: '摩洛哥',
    code: 'MA',
    aliases: ['ma', 'morocco', '摩洛哥', 'MA'],
  },
  '🇩🇿': {
    en: 'Algeria',
    zh: '阿尔及利亚',
    code: 'DZ',
    aliases: ['dz', 'algeria', '阿尔及利亚', '阿爾及利亞', 'DZ'],
  },
  '🇹🇳': {
    en: 'Tunisia',
    zh: '突尼斯',
    code: 'TN',
    aliases: ['tn', 'tunisia', '突尼斯', 'TN'],
  },
  '🇱🇾': {
    en: 'Libya',
    zh: '利比亚',
    code: 'LY',
    aliases: ['ly', 'libya', '利比亚', '利比亞', 'LY'],
  },
  '🇸🇩': {
    en: 'Sudan',
    zh: '苏丹',
    code: 'SD',
    aliases: ['sd', 'sudan', '苏丹', '蘇丹', 'SD'],
  },
  '🇸🇸': {
    en: 'South Sudan',
    zh: '南苏丹',
    code: 'SS',
    aliases: ['ss', 'south sudan', '南苏丹', '南蘇丹', 'SS'],
  },
  '🇨🇩': {
    en: 'Democratic Republic of Congo',
    zh: '刚果民主共和国',
    code: 'CD',
    aliases: ['cd', 'congo', 'drc', '刚果', '剛果', 'CD'],
  },
  '🇨🇬': {
    en: 'Republic of Congo',
    zh: '刚果共和国',
    code: 'CG',
    aliases: ['cg', 'congo', '刚果', '剛果', 'CG'],
  },
  '🇨🇫': {
    en: 'Central African Republic',
    zh: '中非共和国',
    code: 'CF',
    aliases: ['cf', 'central african', '中非', 'CF'],
  },
  '🇨🇲': {
    en: 'Cameroon',
    zh: '喀麦隆',
    code: 'CM',
    aliases: ['cm', 'cameroon', '喀麦隆', '喀麥隆', 'CM'],
  },
  '🇹🇩': {
    en: 'Chad',
    zh: '乍得',
    code: 'TD',
    aliases: ['td', 'chad', '乍得', 'TD'],
  },
  '🇳🇪': {
    en: 'Niger',
    zh: '尼日尔',
    code: 'NE',
    aliases: ['ne', 'niger', '尼日尔', '尼日爾', 'NE'],
  },
  '🇲🇱': {
    en: 'Mali',
    zh: '马里',
    code: 'ML',
    aliases: ['ml', 'mali', '马里', '馬利', 'ML'],
  },
  '🇧🇫': {
    en: 'Burkina Faso',
    zh: '布基纳法索',
    code: 'BF',
    aliases: ['bf', 'burkina', '布基纳法索', '布吉納法索', 'BF'],
  },
  '🇸🇳': {
    en: 'Senegal',
    zh: '塞内加尔',
    code: 'SN',
    aliases: ['sn', 'senegal', '塞内加尔', '塞內加爾', 'SN'],
  },
  '🇬🇲': {
    en: 'Gambia',
    zh: '冈比亚',
    code: 'GM',
    aliases: ['gm', 'gambia', '冈比亚', '甘比亞', 'GM'],
  },
  '🇬🇼': {
    en: 'Guinea-Bissau',
    zh: '几内亚比绍',
    code: 'GW',
    aliases: ['gw', 'guinea-bissau', '几内亚比绍', '幾內亞比索', 'GW'],
  },
  '🇬🇳': {
    en: 'Guinea',
    zh: '几内亚',
    code: 'GN',
    aliases: ['gn', 'guinea', '几内亚', '幾內亞', 'GN'],
  },
  '🇸🇱': {
    en: 'Sierra Leone',
    zh: '塞拉利昂',
    code: 'SL',
    aliases: ['sl', 'sierra leone', '塞拉利昂', 'SL'],
  },
  '🇱🇷': {
    en: 'Liberia',
    zh: '利比里亚',
    code: 'LR',
    aliases: ['lr', 'liberia', '利比里亚', '賴比瑞亞', 'LR'],
  },
  '🇨🇮': {
    en: 'Ivory Coast',
    zh: '科特迪瓦',
    code: 'CI',
    aliases: ['ci', 'ivory coast', '科特迪瓦', '象牙海岸', 'CI'],
  },
  '🇹🇬': {
    en: 'Togo',
    zh: '多哥',
    code: 'TG',
    aliases: ['tg', 'togo', '多哥', 'TG'],
  },
  '🇧🇯': {
    en: 'Benin',
    zh: '贝宁',
    code: 'BJ',
    aliases: ['bj', 'benin', '贝宁', '貝寧', 'BJ'],
  },
  '🇦🇨': {
    en: 'AC',
    zh: 'AC',
    code: 'AC',
    aliases: ['ac', 'AC'],
  },
  '🇦🇩': {
    en: 'AD',
    zh: 'AD',
    code: 'AD',
    aliases: ['ad', 'AD'],
  },
  '🇦🇪': {
    en: 'United Arab Emirates',
    zh: '阿联酋',
    code: 'AE',
    aliases: ['ae', 'AE', 'united arab emirates', '阿联酋'],
  },
  '🇦🇫': {
    en: 'Afghanistan',
    zh: '阿富汗',
    code: 'AF',
    aliases: ['af', 'AF', 'afghanistan', '阿富汗'],
  },
  '🇦🇬': {
    en: 'Antigua and Barbuda',
    zh: '安提瓜和巴布达',
    code: 'AG',
    aliases: ['ag', 'AG', 'antigua and barbuda', '安提瓜和巴布达'],
  },
  '🇦🇮': {
    en: 'Anguilla',
    zh: '安圭拉',
    code: 'AI',
    aliases: ['ai', 'AI', 'anguilla', '安圭拉'],
  },
  '🇦🇱': {
    en: 'Albania',
    zh: '阿尔巴尼亚',
    code: 'AL',
    aliases: ['al', 'AL', 'albania', '阿尔巴尼亚'],
  },
  '🇦🇲': {
    en: 'Armenia',
    zh: '亚美尼亚',
    code: 'AM',
    aliases: ['am', 'AM', 'armenia', '亚美尼亚'],
  },
  '🇦🇴': {
    en: 'Angola',
    zh: '安哥拉',
    code: 'AO',
    aliases: ['ao', 'AO', 'angola', '安哥拉'],
  },
  '🇦🇶': {
    en: 'Antarctica',
    zh: '南极洲',
    code: 'AQ',
    aliases: ['aq', 'AQ', 'antarctica', '南极洲'],
  },
  '🇦🇸': {
    en: 'American Samoa',
    zh: '美属萨摩亚',
    code: 'AS',
    aliases: ['as', 'AS', 'american samoa', '美属萨摩亚'],
  },
  '🇦🇼': {
    en: 'Aruba',
    zh: '阿鲁巴',
    code: 'AW',
    aliases: ['aw', 'AW', 'aruba', '阿鲁巴'],
  },
  '🇦🇽': {
    en: 'Åland Islands',
    zh: '奥兰群岛',
    code: 'AX',
    aliases: ['ax', 'AX', 'åland islands', '奥兰群岛'],
  },
  '🇦🇿': {
    en: 'Azerbaijan',
    zh: '阿塞拜疆',
    code: 'AZ',
    aliases: ['az', 'AZ', 'azerbaijan', '阿塞拜疆'],
  },
  '🇧🇦': {
    en: 'Bosnia and Herzegovina',
    zh: '波黑',
    code: 'BA',
    aliases: ['ba', 'BA', 'bosnia and herzegovina', '波黑'],
  },
  '🇧🇩': {
    en: 'Bangladesh',
    zh: '孟加拉国',
    code: 'BD',
    aliases: ['bd', 'BD', 'bangladesh', '孟加拉国'],
  },
  '🇧🇭': {
    en: 'Bahrain',
    zh: '巴林',
    code: 'BH',
    aliases: ['bh', 'BH', 'bahrain', '巴林'],
  },
  '🇧🇮': {
    en: 'Burundi',
    zh: '布隆迪',
    code: 'BI',
    aliases: ['bi', 'BI', 'burundi', '布隆迪'],
  },
  '🇧🇱': {
    en: 'Saint Barthélemy',
    zh: '圣巴泰勒米',
    code: 'BL',
    aliases: ['bl', 'BL', 'saint barthélemy', '圣巴泰勒米'],
  },
  '🇧🇲': {
    en: 'Bermuda',
    zh: '百慕大',
    code: 'BM',
    aliases: ['bm', 'BM', 'bermuda', '百慕大'],
  },
  '🇧🇶': {
    en: 'Caribbean Netherlands',
    zh: '荷属加勒比',
    code: 'BQ',
    aliases: ['bq', 'BQ', 'caribbean netherlands', '荷属加勒比'],
  },
  '🇧🇹': {
    en: 'Bhutan',
    zh: '不丹',
    code: 'BT',
    aliases: ['bt', 'BT', 'bhutan', '不丹'],
  },
  '🇧🇻': {
    en: 'Bouvet Island',
    zh: '布韦岛',
    code: 'BV',
    aliases: ['bv', 'BV', 'bouvet island', '布韦岛'],
  },
  '🇧🇾': {
    en: 'Belarus',
    zh: '白俄罗斯',
    code: 'BY',
    aliases: ['by', 'BY', 'belarus', '白俄罗斯'],
  },
  '🇨🇨': {
    en: 'Cocos Islands',
    zh: '科科斯群岛',
    code: 'CC',
    aliases: ['cc', 'CC', 'cocos islands', '科科斯群岛'],
  },
  '🇨🇰': {
    en: 'Cook Islands',
    zh: '库克群岛',
    code: 'CK',
    aliases: ['ck', 'CK', 'cook islands', '库克群岛'],
  },
  '🇨🇵': {
    en: 'Clipperton Island',
    zh: '克利珀顿岛',
    code: 'CP',
    aliases: ['cp', 'CP', 'clipperton island', '克利珀顿岛'],
  },
  '🇨🇻': {
    en: 'Cape Verde',
    zh: '佛得角',
    code: 'CV',
    aliases: ['cv', 'CV', 'cape verde', '佛得角'],
  },
  '🇨🇼': {
    en: 'Curaçao',
    zh: '库拉索',
    code: 'CW',
    aliases: ['cw', 'CW', 'curaçao', '库拉索'],
  },
  '🇨🇽': {
    en: 'Christmas Island',
    zh: '圣诞岛',
    code: 'CX',
    aliases: ['cx', 'CX', 'christmas island', '圣诞岛'],
  },
  '🇨🇾': {
    en: 'Cyprus',
    zh: '塞浦路斯',
    code: 'CY',
    aliases: ['cy', 'CY', 'cyprus', '塞浦路斯'],
  },
  '🇩🇬': {
    en: 'Diego Garcia',
    zh: '迪戈加西亚',
    code: 'DG',
    aliases: ['dg', 'DG', 'diego garcia', '迪戈加西亚'],
  },
  '🇩🇯': {
    en: 'Djibouti',
    zh: '吉布提',
    code: 'DJ',
    aliases: ['dj', 'DJ', 'djibouti', '吉布提'],
  },
  '🇩🇰': {
    en: 'Denmark',
    zh: '丹麦',
    code: 'DK',
    aliases: ['dk', 'DK', 'denmark', '丹麦'],
  },
  '🇩🇲': {
    en: 'Dominica',
    zh: '多米尼克',
    code: 'DM',
    aliases: ['dm', 'DM', 'dominica', '多米尼克'],
  },
  '🇪🇦': {
    en: 'Ceuta and Melilla',
    zh: '休达和梅利利亚',
    code: 'EA',
    aliases: ['ea', 'EA', 'ceuta and melilla', '休达和梅利利亚'],
  },
  '🇪🇭': {
    en: 'Western Sahara',
    zh: '西撒哈拉',
    code: 'EH',
    aliases: ['eh', 'EH', 'western sahara', '西撒哈拉'],
  },
  '🇪🇷': {
    en: 'Eritrea',
    zh: '厄立特里亚',
    code: 'ER',
    aliases: ['er', 'ER', 'eritrea', '厄立特里亚'],
  },
  '🇪🇺': {
    en: 'European Union',
    zh: '欧盟',
    code: 'EU',
    aliases: ['eu', 'EU', 'european union', '欧盟'],
  },
  '🇫🇯': {
    en: 'Fiji',
    zh: '斐济',
    code: 'FJ',
    aliases: ['fj', 'FJ', 'fiji', '斐济'],
  },
  '🇫🇲': {
    en: 'Micronesia',
    zh: '密克罗尼西亚',
    code: 'FM',
    aliases: ['fm', 'FM', 'micronesia', '密克罗尼西亚'],
  },
  '🇫🇴': {
    en: 'Faroe Islands',
    zh: '法罗群岛',
    code: 'FO',
    aliases: ['fo', 'FO', 'faroe islands', '法罗群岛'],
  },
  '🇬🇦': {
    en: 'Gabon',
    zh: '加蓬',
    code: 'GA',
    aliases: ['ga', 'GA', 'gabon', '加蓬'],
  },
  '🇬🇩': {
    en: 'Grenada',
    zh: '格林纳达',
    code: 'GD',
    aliases: ['gd', 'GD', 'grenada', '格林纳达'],
  },
  '🇬🇪': {
    en: 'Georgia',
    zh: '格鲁吉亚',
    code: 'GE',
    aliases: ['ge', 'GE', 'georgia', '格鲁吉亚'],
  },
  '🇬🇬': {
    en: 'Guernsey',
    zh: '根西岛',
    code: 'GG',
    aliases: ['gg', 'GG', 'guernsey', '根西岛'],
  },
  '🇬🇮': {
    en: 'Gibraltar',
    zh: '直布罗陀',
    code: 'GI',
    aliases: ['gi', 'GI', 'gibraltar', '直布罗陀'],
  },
  '🇬🇱': {
    en: 'Greenland',
    zh: '格陵兰',
    code: 'GL',
    aliases: ['gl', 'GL', 'greenland', '格陵兰'],
  },
  '🇬🇵': {
    en: 'Guadeloupe',
    zh: '瓜德罗普',
    code: 'GP',
    aliases: ['gp', 'GP', 'guadeloupe', '瓜德罗普'],
  },
  '🇬🇶': {
    en: 'Equatorial Guinea',
    zh: '赤道几内亚',
    code: 'GQ',
    aliases: ['gq', 'GQ', 'equatorial guinea', '赤道几内亚'],
  },
  '🇬🇸': {
    en: 'South Georgia',
    zh: '南乔治亚',
    code: 'GS',
    aliases: ['gs', 'GS', 'south georgia', '南乔治亚'],
  },
  '🇬🇺': {
    en: 'Guam',
    zh: '关岛',
    code: 'GU',
    aliases: ['gu', 'GU', 'guam', '关岛'],
  },
  '🇭🇲': {
    en: 'Heard Island',
    zh: '赫德岛',
    code: 'HM',
    aliases: ['hm', 'HM', 'heard island', '赫德岛'],
  },
  '🇮🇨': {
    en: 'Canary Islands',
    zh: '加那利群岛',
    code: 'IC',
    aliases: ['ic', 'IC', 'canary islands', '加那利群岛'],
  },
  '🇮🇪': {
    en: 'Ireland',
    zh: '爱尔兰',
    code: 'IE',
    aliases: ['ie', 'IE', 'ireland', '爱尔兰'],
  },
  '🇮🇱': {
    en: 'Israel',
    zh: '以色列',
    code: 'IL',
    aliases: ['il', 'IL', 'israel', '以色列'],
  },
  '🇮🇲': {
    en: 'Isle of Man',
    zh: '马恩岛',
    code: 'IM',
    aliases: ['im', 'IM', 'isle of man', '马恩岛'],
  },
  '🇮🇴': {
    en: 'British Indian Ocean Territory',
    zh: '英属印度洋领地',
    code: 'IO',
    aliases: ['io', 'IO', 'british indian ocean territory', '英属印度洋领地'],
  },
  '🇮🇶': {
    en: 'Iraq',
    zh: '伊拉克',
    code: 'IQ',
    aliases: ['iq', 'IQ', 'iraq', '伊拉克'],
  },
  '🇮🇷': {
    en: 'Iran',
    zh: '伊朗',
    code: 'IR',
    aliases: ['ir', 'IR', 'iran', '伊朗'],
  },
  '🇮🇸': {
    en: 'Iceland',
    zh: '冰岛',
    code: 'IS',
    aliases: ['is', 'IS', 'iceland', '冰岛'],
  },
  '🇯🇪': {
    en: 'Jersey',
    zh: '泽西岛',
    code: 'JE',
    aliases: ['je', 'JE', 'jersey', '泽西岛'],
  },
  '🇯🇴': {
    en: 'Jordan',
    zh: '约旦',
    code: 'JO',
    aliases: ['jo', 'JO', 'jordan', '约旦'],
  },
  '🇰🇬': {
    en: 'Kyrgyzstan',
    zh: '吉尔吉斯斯坦',
    code: 'KG',
    aliases: ['kg', 'KG', 'kyrgyzstan', '吉尔吉斯斯坦'],
  },
  '🇰🇮': {
    en: 'Kiribati',
    zh: '基里巴斯',
    code: 'KI',
    aliases: ['ki', 'KI', 'kiribati', '基里巴斯'],
  },
  '🇰🇲': {
    en: 'Comoros',
    zh: '科摩罗',
    code: 'KM',
    aliases: ['km', 'KM', 'comoros', '科摩罗'],
  },
  '🇰🇳': {
    en: 'Saint Kitts and Nevis',
    zh: '圣基茨和尼维斯',
    code: 'KN',
    aliases: ['kn', 'KN', 'saint kitts and nevis', '圣基茨和尼维斯'],
  },
  '🇰🇵': {
    en: 'North Korea',
    zh: '朝鲜',
    code: 'KP',
    aliases: ['kp', 'KP', 'north korea', '朝鲜'],
  },
  '🇰🇼': {
    en: 'Kuwait',
    zh: '科威特',
    code: 'KW',
    aliases: ['kw', 'KW', 'kuwait', '科威特'],
  },
  '🇰🇾': {
    en: 'Cayman Islands',
    zh: '开曼群岛',
    code: 'KY',
    aliases: ['ky', 'KY', 'cayman islands', '开曼群岛'],
  },
  '🇰🇿': {
    en: 'Kazakhstan',
    zh: '哈萨克斯坦',
    code: 'KZ',
    aliases: ['kz', 'KZ', 'kazakhstan', '哈萨克斯坦'],
  },
  '🇱🇧': {
    en: 'Lebanon',
    zh: '黎巴嫩',
    code: 'LB',
    aliases: ['lb', 'LB', 'lebanon', '黎巴嫩'],
  },
  '🇱🇨': {
    en: 'Saint Lucia',
    zh: '圣卢西亚',
    code: 'LC',
    aliases: ['lc', 'LC', 'saint lucia', '圣卢西亚'],
  },
  '🇱🇮': {
    en: 'Liechtenstein',
    zh: '列支敦士登',
    code: 'LI',
    aliases: ['li', 'LI', 'liechtenstein', '列支敦士登'],
  },
  '🇱🇰': {
    en: 'Sri Lanka',
    zh: '斯里兰卡',
    code: 'LK',
    aliases: ['lk', 'LK', 'sri lanka', '斯里兰卡'],
  },
  '🇱🇸': {
    en: 'Lesotho',
    zh: '莱索托',
    code: 'LS',
    aliases: ['ls', 'LS', 'lesotho', '莱索托'],
  },
  '🇱🇺': {
    en: 'Luxembourg',
    zh: '卢森堡',
    code: 'LU',
    aliases: ['lu', 'LU', 'luxembourg', '卢森堡'],
  },
  '🇲🇨': {
    en: 'Monaco',
    zh: '摩纳哥',
    code: 'MC',
    aliases: ['mc', 'MC', 'monaco', '摩纳哥'],
  },
  '🇲🇩': {
    en: 'Moldova',
    zh: '摩尔多瓦',
    code: 'MD',
    aliases: ['md', 'MD', 'moldova', '摩尔多瓦'],
  },
  '🇲🇪': {
    en: 'Montenegro',
    zh: '黑山',
    code: 'ME',
    aliases: ['me', 'ME', 'montenegro', '黑山'],
  },
  '🇲🇫': {
    en: 'Saint Martin',
    zh: '法属圣马丁',
    code: 'MF',
    aliases: ['mf', 'MF', 'saint martin', '法属圣马丁'],
  },
  '🇲🇬': {
    en: 'Madagascar',
    zh: '马达加斯加',
    code: 'MG',
    aliases: ['mg', 'MG', 'madagascar', '马达加斯加'],
  },
  '🇲🇭': {
    en: 'Marshall Islands',
    zh: '马绍尔群岛',
    code: 'MH',
    aliases: ['mh', 'MH', 'marshall islands', '马绍尔群岛'],
  },
  '🇲🇰': {
    en: 'North Macedonia',
    zh: '北马其顿',
    code: 'MK',
    aliases: ['mk', 'MK', 'north macedonia', '北马其顿'],
  },
  '🇲🇳': {
    en: 'Mongolia',
    zh: '蒙古',
    code: 'MN',
    aliases: ['mn', 'MN', 'mongolia', '蒙古'],
  },
  '🇲🇴': {
    en: 'Macau',
    zh: '澳门',
    code: 'MO',
    aliases: ['mo', 'MO', 'macau', '澳门'],
  },
  '🇲🇵': {
    en: 'Northern Mariana Islands',
    zh: '北马里亚纳群岛',
    code: 'MP',
    aliases: ['mp', 'MP', 'northern mariana islands', '北马里亚纳群岛'],
  },
  '🇲🇶': {
    en: 'Martinique',
    zh: '马提尼克',
    code: 'MQ',
    aliases: ['mq', 'MQ', 'martinique', '马提尼克'],
  },
  '🇲🇷': {
    en: 'Mauritania',
    zh: '毛里塔尼亚',
    code: 'MR',
    aliases: ['mr', 'MR', 'mauritania', '毛里塔尼亚'],
  },
  '🇲🇸': {
    en: 'Montserrat',
    zh: '蒙特塞拉特',
    code: 'MS',
    aliases: ['ms', 'MS', 'montserrat', '蒙特塞拉特'],
  },
  '🇲🇹': {
    en: 'Malta',
    zh: '马耳他',
    code: 'MT',
    aliases: ['mt', 'MT', 'malta', '马耳他'],
  },
  '🇲🇺': {
    en: 'Mauritius',
    zh: '毛里求斯',
    code: 'MU',
    aliases: ['mu', 'MU', 'mauritius', '毛里求斯'],
  },
  '🇲🇻': {
    en: 'Maldives',
    zh: '马尔代夫',
    code: 'MV',
    aliases: ['mv', 'MV', 'maldives', '马尔代夫'],
  },
  '🇲🇼': {
    en: 'Malawi',
    zh: '马拉维',
    code: 'MW',
    aliases: ['mw', 'MW', 'malawi', '马拉维'],
  },
  '🇲🇿': {
    en: 'Mozambique',
    zh: '莫桑比克',
    code: 'MZ',
    aliases: ['mz', 'MZ', 'mozambique', '莫桑比克'],
  },
  '🇳🇨': {
    en: 'New Caledonia',
    zh: '新喀里多尼亚',
    code: 'NC',
    aliases: ['nc', 'NC', 'new caledonia', '新喀里多尼亚'],
  },
  '🇳🇫': {
    en: 'Norfolk Island',
    zh: '诺福克岛',
    code: 'NF',
    aliases: ['nf', 'NF', 'norfolk island', '诺福克岛'],
  },
  '🇳🇵': {
    en: 'Nepal',
    zh: '尼泊尔',
    code: 'NP',
    aliases: ['np', 'NP', 'nepal', '尼泊尔'],
  },
  '🇳🇷': {
    en: 'Nauru',
    zh: '瑙鲁',
    code: 'NR',
    aliases: ['nr', 'NR', 'nauru', '瑙鲁'],
  },
  '🇳🇺': {
    en: 'Niue',
    zh: '纽埃',
    code: 'NU',
    aliases: ['nu', 'NU', 'niue', '纽埃'],
  },
  '🇳🇿': {
    en: 'New Zealand',
    zh: '新西兰',
    code: 'NZ',
    aliases: ['nz', 'NZ', 'new zealand', '新西兰'],
  },
  '🇴🇲': {
    en: 'Oman',
    zh: '阿曼',
    code: 'OM',
    aliases: ['om', 'OM', 'oman', '阿曼'],
  },
  '🇵🇫': {
    en: 'French Polynesia',
    zh: '法属波利尼西亚',
    code: 'PF',
    aliases: ['pf', 'PF', 'french polynesia', '法属波利尼西亚'],
  },
  '🇵🇬': {
    en: 'Papua New Guinea',
    zh: '巴布亚新几内亚',
    code: 'PG',
    aliases: ['pg', 'PG', 'papua new guinea', '巴布亚新几内亚'],
  },
  '🇵🇰': {
    en: 'Pakistan',
    zh: '巴基斯坦',
    code: 'PK',
    aliases: ['pk', 'PK', 'pakistan', '巴基斯坦'],
  },
  '🇵🇲': {
    en: 'Saint Pierre and Miquelon',
    zh: '圣皮埃尔和密克隆',
    code: 'PM',
    aliases: ['pm', 'PM', 'saint pierre and miquelon', '圣皮埃尔和密克隆'],
  },
  '🇵🇳': {
    en: 'Pitcairn Islands',
    zh: '皮特凯恩群岛',
    code: 'PN',
    aliases: ['pn', 'PN', 'pitcairn islands', '皮特凯恩群岛'],
  },
  '🇵🇷': {
    en: 'Puerto Rico',
    zh: '波多黎各',
    code: 'PR',
    aliases: ['pr', 'PR', 'puerto rico', '波多黎各'],
  },
  '🇵🇸': {
    en: 'Palestine',
    zh: '巴勒斯坦',
    code: 'PS',
    aliases: ['ps', 'PS', 'palestine', '巴勒斯坦'],
  },
  '🇵🇼': {
    en: 'Palau',
    zh: '帕劳',
    code: 'PW',
    aliases: ['pw', 'PW', 'palau', '帕劳'],
  },
  '🇶🇦': {
    en: 'Qatar',
    zh: '卡塔尔',
    code: 'QA',
    aliases: ['qa', 'QA', 'qatar', '卡塔尔'],
  },
  '🇷🇪': {
    en: 'Réunion',
    zh: '留尼汪',
    code: 'RE',
    aliases: ['re', 'RE', 'réunion', '留尼汪'],
  },
  '🇷🇸': {
    en: 'Serbia',
    zh: '塞尔维亚',
    code: 'RS',
    aliases: ['rs', 'RS', 'serbia', '塞尔维亚'],
  },
  '🇸🇦': {
    en: 'Saudi Arabia',
    zh: '沙特阿拉伯',
    code: 'SA',
    aliases: ['sa', 'SA', 'saudi arabia', '沙特阿拉伯'],
  },
  '🇸🇧': {
    en: 'Solomon Islands',
    zh: '所罗门群岛',
    code: 'SB',
    aliases: ['sb', 'SB', 'solomon islands', '所罗门群岛'],
  },
  '🇸🇨': {
    en: 'Seychelles',
    zh: '塞舌尔',
    code: 'SC',
    aliases: ['sc', 'SC', 'seychelles', '塞舌尔'],
  },
  '🇸🇭': {
    en: 'Saint Helena',
    zh: '圣赫勒拿',
    code: 'SH',
    aliases: ['sh', 'SH', 'saint helena', '圣赫勒拿'],
  },
  '🇸🇯': {
    en: 'Svalbard and Jan Mayen',
    zh: '斯瓦尔巴和扬马延',
    code: 'SJ',
    aliases: ['sj', 'SJ', 'svalbard and jan mayen', '斯瓦尔巴和扬马延'],
  },
  '🇸🇲': {
    en: 'San Marino',
    zh: '圣马力诺',
    code: 'SM',
    aliases: ['sm', 'SM', 'san marino', '圣马力诺'],
  },
  '🇸🇴': {
    en: 'Somalia',
    zh: '索马里',
    code: 'SO',
    aliases: ['so', 'SO', 'somalia', '索马里'],
  },
  '🇸🇹': {
    en: 'São Tomé and Príncipe',
    zh: '圣多美和普林西比',
    code: 'ST',
    aliases: ['st', 'ST', 'são tomé and príncipe', '圣多美和普林西比'],
  },
  '🇸🇽': {
    en: 'Sint Maarten',
    zh: '荷属圣马丁',
    code: 'SX',
    aliases: ['sx', 'SX', 'sint maarten', '荷属圣马丁'],
  },
  '🇸🇾': {
    en: 'Syria',
    zh: '叙利亚',
    code: 'SY',
    aliases: ['sy', 'SY', 'syria', '叙利亚'],
  },
  '🇸🇿': {
    en: 'Eswatini',
    zh: '斯威士兰',
    code: 'SZ',
    aliases: ['sz', 'SZ', 'eswatini', '斯威士兰'],
  },
  '🇹🇦': {
    en: 'Tristan da Cunha',
    zh: '特里斯坦 - 达库尼亚',
    code: 'TA',
    aliases: ['ta', 'TA', 'tristan da cunha', '特里斯坦 - 达库尼亚'],
  },
  '🇹🇨': {
    en: 'Turks and Caicos Islands',
    zh: '特克斯和凯科斯群岛',
    code: 'TC',
    aliases: ['tc', 'TC', 'turks and caicos islands', '特克斯和凯科斯群岛'],
  },
  '🇹🇫': {
    en: 'French Southern Territories',
    zh: '法属南部领地',
    code: 'TF',
    aliases: ['tf', 'TF', 'french southern territories', '法属南部领地'],
  },
  '🇹🇯': {
    en: 'Tajikistan',
    zh: '塔吉克斯坦',
    code: 'TJ',
    aliases: ['tj', 'TJ', 'tajikistan', '塔吉克斯坦'],
  },
  '🇹🇰': {
    en: 'Tokelau',
    zh: '托克劳',
    code: 'TK',
    aliases: ['tk', 'TK', 'tokelau', '托克劳'],
  },
  '🇹🇱': {
    en: 'Timor-Leste',
    zh: '东帝汶',
    code: 'TL',
    aliases: ['tl', 'TL', 'timor-leste', '东帝汶'],
  },
  '🇹🇲': {
    en: 'Turkmenistan',
    zh: '土库曼斯坦',
    code: 'TM',
    aliases: ['tm', 'TM', 'turkmenistan', '土库曼斯坦'],
  },
  '🇹🇴': {
    en: 'Tonga',
    zh: '汤加',
    code: 'TO',
    aliases: ['to', 'TO', 'tonga', '汤加'],
  },
  '🇹🇻': {
    en: 'Tuvalu',
    zh: '图瓦卢',
    code: 'TV',
    aliases: ['tv', 'TV', 'tuvalu', '图瓦卢'],
  },
  '🇺🇦': {
    en: 'Ukraine',
    zh: '乌克兰',
    code: 'UA',
    aliases: ['ua', 'UA', 'ukraine', '乌克兰'],
  },
  '🇺🇲': {
    en: 'U.S. Minor Outlying Islands',
    zh: '美国本土外小岛屿',
    code: 'UM',
    aliases: ['um', 'UM', 'u.s. minor outlying islands', '美国本土外小岛屿'],
  },
  '🇺🇳': {
    en: 'United Nations',
    zh: '联合国',
    code: 'UN',
    aliases: ['un', 'UN', 'united nations', '联合国'],
  },
  '🇺🇿': {
    en: 'Uzbekistan',
    zh: '乌兹别克斯坦',
    code: 'UZ',
    aliases: ['uz', 'UZ', 'uzbekistan', '乌兹别克斯坦'],
  },
  '🇻🇦': {
    en: 'Vatican City',
    zh: '梵蒂冈',
    code: 'VA',
    aliases: ['va', 'VA', 'vatican city', '梵蒂冈'],
  },
  '🇻🇨': {
    en: 'Saint Vincent and the Grenadines',
    zh: '圣文森特和格林纳丁斯',
    code: 'VC',
    aliases: ['vc', 'VC', 'saint vincent and the grenadines', '圣文森特和格林纳丁斯'],
  },
  '🇻🇬': {
    en: 'British Virgin Islands',
    zh: '英属维尔京群岛',
    code: 'VG',
    aliases: ['vg', 'VG', 'british virgin islands', '英属维尔京群岛'],
  },
  '🇻🇮': {
    en: 'U.S. Virgin Islands',
    zh: '美属维尔京群岛',
    code: 'VI',
    aliases: ['vi', 'VI', 'u.s. virgin islands', '美属维尔京群岛'],
  },
  '🇻🇺': {
    en: 'Vanuatu',
    zh: '瓦努阿图',
    code: 'VU',
    aliases: ['vu', 'VU', 'vanuatu', '瓦努阿图'],
  },
  '🇼🇫': {
    en: 'Wallis and Futuna',
    zh: '瓦利斯和富图纳',
    code: 'WF',
    aliases: ['wf', 'WF', 'wallis and futuna', '瓦利斯和富图纳'],
  },
  '🇼🇸': {
    en: 'Samoa',
    zh: '萨摩亚',
    code: 'WS',
    aliases: ['ws', 'WS', 'samoa', '萨摩亚'],
  },
  '🇽🇰': {
    en: 'Kosovo',
    zh: '科索沃',
    code: 'XK',
    aliases: ['xk', 'XK', 'kosovo', '科索沃'],
  },
  '🇾🇪': {
    en: 'Yemen',
    zh: '也门',
    code: 'YE',
    aliases: ['ye', 'YE', 'yemen', '也门'],
  },
  '🇾🇹': {
    en: 'Mayotte',
    zh: '马约特',
    code: 'YT',
    aliases: ['yt', 'YT', 'mayotte', '马约特'],
  },
}

interface RegionEntry { emoji: string, en: string, zh: string, code: string, aliases: string[] }

const CJK_UNIFIED_IDEOGRAPH_REGEX = /\p{Script=Han}/u

function getRegionEntry(region: string | null | undefined): RegionEntry | null {
  if (!region?.trim())
    return null

  const trimmed = region.trim()
  const direct = emojiToRegionMap[trimmed]
  if (direct)
    return { emoji: trimmed, en: direct.en, zh: direct.zh, code: direct.code, aliases: direct.aliases }

  const lowerRegion = trimmed.toLowerCase()
  for (const [emoji, info] of Object.entries(emojiToRegionMap)) {
    if (info.code.toLowerCase() === lowerRegion || info.aliases.some(alias => alias.toLowerCase() === lowerRegion))
      return { emoji, en: info.en, zh: info.zh, code: info.code, aliases: info.aliases }
  }

  return null
}

/**
 * 检查地区emoji是否匹配搜索词
 * @param regionEmoji 地区emoji（如：🇭🇰）
 * @param searchTerm 搜索词
 * @returns 是否匹配
 */
export function isRegionMatch(regionEmoji: string, searchTerm: string): boolean {
  const lowerSearchTerm = searchTerm.toLowerCase().trim()

  // 直接匹配emoji / 原始值，保留未知地区的搜索能力
  if (regionEmoji === searchTerm)
    return true

  const regionInfo = getRegionEntry(regionEmoji)
  if (!regionInfo)
    return regionEmoji.toLowerCase().includes(lowerSearchTerm)

  // 检查英文名称
  if (regionInfo.en.toLowerCase().includes(lowerSearchTerm))
    return true

  // 检查中文名称
  if (regionInfo.zh.includes(lowerSearchTerm))
    return true

  // 检查别名
  return regionInfo.aliases.some(alias =>
    alias.toLowerCase().includes(lowerSearchTerm),
  )
}

/**
 * 获取地区的显示名称
 * @param regionEmoji 地区emoji
 * @param language 语言 ('en' | 'zh')
 * @returns 地区名称
 */
export function getRegionDisplayName(regionEmoji: string, language: 'en' | 'zh' = 'zh'): string {
  const regionInfo = getRegionEntry(regionEmoji)
  if (!regionInfo)
    return ''

  if (language === 'en')
    return regionInfo.en

  return CJK_UNIFIED_IDEOGRAPH_REGEX.test(regionInfo.zh) ? regionInfo.zh : ''
}

/**
 * 获取所有支持的地区emoji列表
 * @returns 地区emoji数组
 */
export function getSupportedRegions(): string[] {
  return Object.keys(emojiToRegionMap)
}

/**
 * 获取地区代码
 * @param regionEmoji 地区emoji
 * @returns 地区代码（如：HK, CN, US）
 */
export function getRegionCode(regionEmoji: string): string {
  const regionInfo = getRegionEntry(regionEmoji)
  if (!regionInfo)
    return regionEmoji

  return regionInfo.code
}

/**
 * 根据地区代码获取emoji
 * @param code 地区代码（如：HK, CN, US）
 * @returns 地区emoji
 */
export function getEmojiByCode(code: string): string {
  const upperCode = code.toUpperCase()
  for (const [emoji, info] of Object.entries(emojiToRegionMap)) {
    if (info.code === upperCode) {
      return emoji
    }
  }
  return code
}

/**
 * 根据搜索词查找匹配的地区
 * @param searchTerm 搜索词
 * @returns 匹配的地区信息数组
 */
export function searchRegions(searchTerm: string): Array<{ emoji: string, en: string, zh: string, code: string }> {
  const results: Array<{ emoji: string, en: string, zh: string, code: string }> = []
  const lowerSearchTerm = searchTerm.toLowerCase().trim()

  for (const [emoji, info] of Object.entries(emojiToRegionMap)) {
    if (isRegionMatch(emoji, lowerSearchTerm)) {
      results.push({
        emoji,
        en: info.en,
        zh: info.zh,
        code: info.code,
      })
    }
  }

  return results
}

/**
 * 根据别名或代码获取地区信息
 * @param aliasOrCode 别名或代码（如：hk, HK, 香港, hongkong）
 * @returns 地区信息，如果未找到则返回 null
 */
export function getRegionByAlias(aliasOrCode: string): { emoji: string, en: string, zh: string, code: string } | null {
  const regionInfo = getRegionEntry(aliasOrCode)
  if (!regionInfo)
    return null

  return { emoji: regionInfo.emoji, en: regionInfo.en, zh: regionInfo.zh, code: regionInfo.code }
}

/**
 * 从文本中提取地区emoji
 * @param text 包含地区emoji的文本
 * @returns 提取到的地区emoji数组
 */
export function extractRegionEmojis(text: string): string[] {
  const emojis: string[] = []
  // 匹配国旗emoji（由两个区域指示符字符组成）
  const matches = text.match(REGION_FLAG_REGEX)

  if (matches) {
    for (const match of matches) {
      if (emojiToRegionMap[match]) {
        emojis.push(match)
      }
    }
  }

  return emojis
}
