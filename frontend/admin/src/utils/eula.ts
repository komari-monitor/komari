import { resolveI18nText } from "@/utils/i18nText";

// 法律声明与合规指引全文（按语言本地化）
// EULA full text, localized per language.
export const EULAS: Record<string, string> = {
  "zh_CN": `法律声明与合规指引

重要提示
  本文档旨在明确 Komari（“本软件”）的合法使用边界、用户权利义务与风险提示。请在下载、安装或使用本软件前，务必完整阅读并理解本声明的全部内容。一经下载、安装或使用，即视为您已理解并同意受本声明约束。

1. 适用范围
  本声明适用于所有直接或间接获取、安装、访问或使用本软件及其衍生工具、文档与服务的自然人、法人或其他组织。

2. 定义
  - “您/用户”：指任何以任何方式使用本软件的主体。
  - “本软件/Komari”：指开源发布的 Komari Monitor 及相关组件、示例、脚本与文档。
  - “目标设备”：指被您管理或操作的任何主机、服务器、虚拟机、容器或网络设备。

3. 许可与使用边界
  - 本软件依据开源许可证（MIT）授权使用。除 MIT 许可另有规定外，本声明作为合规与风险提示的补充条款。
  - 本软件仅用于合法、合规、经授权的系统管理、监测和研究目的。不得将本软件用于任何违反法律法规、监管政策或第三方合法权益之行为。
  - 本软件不提供、亦不包含任何绕过安全控制、破解或渗透测试的隐含授权。进行安全测试前，您必须取得被测系统的事先明确书面授权。

4. 使用者责任（极其重要）
  您对通过本软件进行的所有操作承担全部且唯一的法律责任。无论您的使用意图为何，只要行为发生，所产生的一切后果均由您自行承担，与本软件开发者无关。开发者不对您的使用方式进行审查、监控或背书，也不承担任何直接、间接或连带责任。

5. 严禁行为（包括但不限于）
  5.1 未经授权的入侵、控制或访问任何计算机、服务器、网络或账户。
  5.2 扫描端口、探测漏洞或从事任何未经授权的安全测试与信息收集。
  5.3 发起或参与网络攻击（如 DoS/DDoS、流量劫持、嗅探、中间人攻击等）。
  5.4 安装、分发或执行恶意代码、木马、后门、勒索软件或僵尸网络程序；利用被控设备进行挖矿、垃圾邮件发送、钓鱼或诈骗。
  5.5 未经授权收集、处理、篡改、删除或泄露个人信息、商业秘密、知识产权或其他敏感数据；进行屏幕窥视、键盘记录、文件监听等监控行为。
  5.6 传播任何违法或侵权内容，包括但不限于淫秽、暴力、恐怖主义、仇恨、歧视、诽谤或其他不当内容。
  5.7 规避技术保护措施、逆向工程（法律允许范围除外）、干扰/破坏他人系统稳定性与可用性。

6. 合规与地区要求（特别说明）
  您必须确保您的使用行为同时符合：
  - 您所在地法律法规；
  - 目标设备所在地法律法规；
  - 相关行业监管规则与自律规范。
  特别地，若您位于中国大陆地区，须严格遵守《网络安全法》《数据安全法》《个人信息保护法》《关键信息基础设施安全保护条例》等相关法律法规与标准。若您位于香港特别行政区、澳门特别行政区及台湾地区，亦须遵守当地适用之法律法规。任何违法或违规风险由您自行承担。

7. 数据与隐私合规
  - 处理任何个人信息前，您应具备合法、正当、必要之处理依据，并履行告知、同意、最小化、目的限制、安全保障与跨境合规等义务。
  - 若涉及敏感数据或机密信息，您应实施加密、脱敏、访问控制、日志审计等合理、必要的技术与管理措施。
  - 为符合法律合规、安全审计、防滥用与争议处理之目的，在适用法律允许且必要时，我们可能会收集并保留最小化的网络元数据。该数据仅用于安全与法务合规用途，除法律纠纷外，我们不会主动分享您的数据。
  - 数据安全与加密：我们在合理可行范围内采取行业通行的安全措施，以降低数据泄露、篡改与丢失风险。
  - 除非基于法律法规要求、执法或监管部门的合法请求，或为保护我们及用户的合法权益所必需，我们不会向第三方披露前述数据。

8. 安全、内容与知识产权合规
  - 您应确保对目标设备具有合法的管理权限或操作授权。
  - 不得利用本软件侵犯任何第三方的知识产权、名誉权、隐私权或其他合法权益。
  - 对于由您接入或展示的内容，您独立承担合规与版权责任。

9. 第三方组件与服务
  本软件可能依赖第三方开源库或外部服务。该等第三方的可用性、准确性与合规性由其各自提供方负责。您应遵循相应许可或使用条款，并自行评估与承担相关风险。

10. 风险提示
  - 本软件以“现状”提供，可能受限于网络、硬件、系统差异而产生不兼容、不可用或误用风险。
  - 远程控制与批量操作具有潜在高风险，请务必采取最小权限、分级授权、多因素认证、审计留痕、分环境验证等最佳实践。

11. 免责声明
  在适用法律允许的最大范围内：本软件及其开发者不对本软件的适用性、稳定性、正确性、可用性或特定目的适配性作出任何明示或默示保证；亦不对因使用或无法使用本软件而导致的任何形式的损失或损害承担责任。

12. 责任限制
  在任何情况下，本软件开发者对您因本软件产生或与之相关的任何间接、偶然、特殊、惩罚性或后果性损害不承担责任；对任何直接损失的总责任（如有）以适用法律允许的最低上限为准。

13. 赔偿条款
  若您因违反本声明或适用法律而引发任何第三方主张、索赔、纠纷或处罚，您应独立承担全部责任，并使本软件开发者及其贡献者免受损害。

14. 终止与技术支持
  对于任何涉嫌或实际违反本声明的用户，开发者有权拒绝或终止提供任何形式的技术支持或协助。

15. 出口管制与制裁合规
  您承诺遵守适用的出口管制、再出口与经济制裁法律法规，不得将本软件用于或提供给受限制的国家、地区、实体或个人。

16. 通知与修订
  本声明可能随版本更新或法律政策变化进行修订。更新后的版本将以适当方式公布并自公布之日起生效。

17. 适用法律与争议解决
  在不抵触强制性法律的前提下，本声明的解释与适用以本软件开源仓库维护者所在地法律为准；争议应友好协商解决，协商不成的，提交有管辖权的法院或仲裁机构处理。

18. 最终条款
  若您不同意本声明任何内容，或无法确保您的使用完全合法合规，请立即停止使用并卸载本软件。继续使用即视为您已阅读、理解并同意本声明全部内容。

生效日期：2025-10-20
`,
  "zh_TW": `法律聲明與合規指引

重要提示
  本文件旨在明確 Komari（“本軟體”）的合法使用邊界、使用者權利義務與風險提示。請在下載、安裝或使用本軟體前，務必完整閱讀並理解本聲明的全部內容。一經下載、安裝或使用，即視為您已理解並同意受本聲明約束。

1. 適用範圍
  本聲明適用於所有直接或間接獲取、安裝、訪問或使用本軟體及其衍生工具、文件與服務的自然人、法人或其他組織。

2. 定義
  - “您/使用者”：指任何以任何方式使用本軟體的主體。
  - “本軟體/Komari”：指開源釋出的 Komari Monitor 及相關元件、示例、指令碼與文件。
  - “目標裝置”：指被您管理或操作的任何主機、伺服器、虛擬機器、容器或網路裝置。

3. 許可與使用邊界
  - 本軟體依據開源許可證（MIT）授權使用。除 MIT 許可另有規定外，本聲明作為合規與風險提示的補充條款。
  - 本軟體僅用於合法、合規、經授權的系統管理、監測和研究目的。不得將本軟體用於任何違反法律法規、監管政策或第三方合法權益之行為。
  - 本軟體不提供、亦不包含任何繞過安全控制、破解或滲透測試的隱含授權。進行安全測試前，您必須取得被測系統的事先明確書面授權。

4. 使用者責任（極其重要）
  您對透過本軟體進行的所有操作承擔全部且唯一的法律責任。無論您的使用意圖為何，只要行為發生，所產生的一切後果均由您自行承擔，與本軟體開發者無關。開發者不對您的使用方式進行審查、監控或背書，也不承擔任何直接、間接或連帶責任。

5. 嚴禁行為（包括但不限於）
  5.1 未經授權的入侵、控制或訪問任何電腦、伺服器、網路或帳戶。
  5.2 掃描埠、探測漏洞或從事任何未經授權的安全測試與資訊收集。
  5.3 發起或參與網路攻擊（如 DoS/DDoS、流量劫持、嗅探、中間人攻擊等）。
  5.4 安裝、分發或執行惡意程式碼、木馬、後門、勒索軟體或殭屍網路程式；利用被控裝置進行挖礦、垃圾郵件傳送、釣魚或詐騙。
  5.5 未經授權收集、處理、篡改、刪除或洩露個人資訊、商業秘密、智慧財產權或其他敏感資料；進行螢幕窺視、鍵盤記錄、檔案監聽等監控行為。
  5.6 傳播任何違法或侵權內容，包括但不限於淫穢、暴力、恐怖主義、仇恨、歧視、誹謗或其他不當內容。
  5.7 規避技術保護措施、逆向工程（法律允許範圍除外）、干擾/破壞他人系統穩定性與可用性。

6. 合規與地區要求（特別說明）
  您必須確保您的使用行為同時符合：
  - 您所在地法律法規；
  - 目標裝置所在地法律法規；
  - 相關行業監管規則與自律規範。
  特別地，若您位於中國大陸地區，須嚴格遵守《網路安全法》《資料安全法》《個人資訊保護法》《關鍵資訊基礎設施安全保護條例》等相關法律法規與標準。若您位於香港特別行政區、澳門特別行政區及臺灣地區，亦須遵守當地適用之法律法規。任何違法或違規風險由您自行承擔。

7. 資料與隱私合規
  - 處理任何個人資訊前，您應具備合法、正當、必要之處理依據，並履行告知、同意、最小化、目的限制、安全保障與跨境合規等義務。
  - 若涉及敏感資料或機密資訊，您應實施加密、脫敏、訪問控制、日誌審計等合理、必要的技術與管理措施。
  - 為符合法律合規、安全審計、防濫用與爭議處理之目的，在適用法律允許且必要時，我們可能會收集並保留最小化的網路後設資料。該資料僅用於安全與法務合規用途，除法律糾紛外，我們不會主動分享您的資料。
  - 資料安全與加密：我們在合理可行範圍內採取行業通行的安全措施，以降低資料洩露、篡改與丟失風險。
  - 除非基於法律法規要求、執法或監管部門的合法請求，或為保護我們及使用者的合法權益所必需，我們不會向第三方披露前述資料。

8. 安全、內容與智慧財產權合規
  - 您應確保對目標裝置具有合法的管理權限或操作授權。
  - 不得利用本軟體侵犯任何第三方的智慧財產權、名譽權、隱私權或其他合法權益。
  - 對於由您接入或展示的內容，您獨立承擔合規與版權責任。

9. 第三方元件與服務
  本軟體可能依賴第三方開源庫或外部服務。該等第三方的可用性、準確性與合規性由其各自提供方負責。您應遵循相應許可或使用條款，並自行評估與承擔相關風險。

10. 風險提示
  - 本軟體以“現狀”提供，可能受限於網路、硬體、系統差異而產生不相容、不可用或誤用風險。
  - 遠端控制與批次操作具有潛在高風險，請務必採取最小權限、分級授權、多因素認證、審計留痕、分環境驗證等最佳實踐。

11. 免責聲明
  在適用法律允許的最大範圍內：本軟體及其開發者不對本軟體的適用性、穩定性、正確性、可用性或特定目的適配性作出任何明示或默示保證；亦不對因使用或無法使用本軟體而導致的任何形式的損失或損害承擔責任。

12. 責任限制
  在任何情況下，本軟體開發者對您因本軟體產生或與之相關的任何間接、偶然、特殊、懲罰性或後果性損害不承擔責任；對任何直接損失的總責任（如有）以適用法律允許的最低上限為準。

13. 賠償條款
  若您因違反本聲明或適用法律而引發任何第三方主張、索賠、糾紛或處罰，您應獨立承擔全部責任，並使本軟體開發者及其貢獻者免受損害。

14. 終止與技術支援
  對於任何涉嫌或實際違反本聲明的使用者，開發者有權拒絕或終止提供任何形式的技術支援或協助。

15. 出口管制與制裁合規
  您承諾遵守適用的出口管制、再出口與經濟制裁法律法規，不得將本軟體用於或提供給受限制的國家、地區、實體或個人。

16. 通知與修訂
  本聲明可能隨版本更新或法律政策變化進行修訂。更新後的版本將以適當方式公佈並自公佈之日起生效。

17. 適用法律與爭議解決
  在不牴觸強制性法律的前提下，本聲明的解釋與適用以本軟體開源倉庫維護者所在地法律為準；爭議應友好協商解決，協商不成的，提交有管轄權的法院或仲裁機構處理。

18. 最終條款
  若您不同意本聲明任何內容，或無法確保您的使用完全合法合規，請立即停止使用並解除安裝本軟體。繼續使用即視為您已閱讀、理解並同意本聲明全部內容。

生效日期：2025-10-20
`,
  "en": `Legal Notice and Compliance Guide

Important Notice
  This document clarifies the lawful boundaries of using Komari ("the Software"), your rights and obligations, and risk disclosures. Before downloading, installing, or using the Software, please read and understand this notice in full. By downloading, installing, or using the Software, you acknowledge that you have read, understood, and agreed to be bound by this notice.

1. Scope of Application
  This notice applies to all individuals, legal persons, or other organizations that directly or indirectly obtain, install, access, or use the Software and its derivative tools, documentation, and services.

2. Definitions
  - "You/User": any party that uses the Software in any manner.
  - "Software/Komari": Komari Monitor, released as open source, and its related components, examples, scripts, and documentation.
  - "Target Device": any host, server, virtual machine, container, or network device that you manage or operate.

3. License and Usage Boundaries
  - The Software is licensed under the MIT open-source license. Except as otherwise provided by the MIT license, this notice serves as supplementary terms for compliance and risk disclosure.
  - The Software is intended solely for lawful, compliant, and authorized system administration, monitoring, and research purposes. You must not use the Software for any activity that violates laws, regulations, regulatory policies, or the legitimate rights of third parties.
  - The Software does not provide or imply any authorization to bypass security controls, crack, or perform penetration testing. Before conducting security testing, you must obtain prior explicit written authorization from the owner of the system being tested.

4. User Responsibility (Extremely Important)
  You bear full and sole legal responsibility for all operations performed through the Software. Regardless of your intent, all consequences arising from such actions are your sole responsibility and are not attributable to the developers of the Software. The developers do not review, monitor, or endorse how you use the Software and assume no direct, indirect, or joint liability.

5. Prohibited Conduct (Including but Not Limited To)
  5.1 Unauthorized intrusion, control, or access to any computer, server, network, or account.
  5.2 Port scanning, vulnerability probing, or any unauthorized security testing and information gathering.
  5.3 Initiating or participating in cyberattacks (e.g., DoS/DDoS, traffic hijacking, sniffing, man-in-the-middle attacks).
  5.4 Installing, distributing, or executing malicious code, trojans, backdoors, ransomware, or botnet programs; using controlled devices for cryptocurrency mining, spam, phishing, or fraud.
  5.5 Unauthorized collection, processing, tampering, deletion, or disclosure of personal information, trade secrets, intellectual property, or other sensitive data; conducting monitoring such as screen surveillance, keystroke logging, or file interception.
  5.6 Disseminating any illegal or infringing content, including but not limited to obscene, violent, terrorist, hateful, discriminatory, defamatory, or other inappropriate content.
  5.7 Circumventing technical protection measures, reverse engineering (except as permitted by law), or interfering with/destroying the stability and availability of others' systems.

6. Compliance and Regional Requirements (Special Note)
  You must ensure that your use complies with:
  - the laws and regulations of your location;
  - the laws and regulations of the location of the target devices;
  - relevant industry regulatory rules and self-regulatory standards.
  In particular, if you are located in mainland China, you must strictly comply with the Cybersecurity Law, the Data Security Law, the Personal Information Protection Law, the Regulations on the Security Protection of Critical Information Infrastructure, and other relevant laws, regulations, and standards. If you are located in the Hong Kong Special Administrative Region, the Macao Special Administrative Region, or the Taiwan region, you must also comply with applicable local laws and regulations. You bear all risks arising from any illegal or non-compliant conduct.

7. Data and Privacy Compliance
  - Before processing any personal information, you must have a lawful, legitimate, and necessary basis for processing and fulfill obligations such as notice, consent, minimization, purpose limitation, security safeguards, and cross-border compliance.
  - If sensitive or confidential data is involved, you must implement reasonable and necessary technical and administrative measures such as encryption, desensitization, access control, and log auditing.
  - For purposes of legal compliance, security auditing, abuse prevention, and dispute resolution, and where permitted and necessary under applicable law, we may collect and retain minimal network metadata. Such data is used solely for security and legal compliance purposes, and we will not proactively share your data except in the context of legal disputes.
  - Data security and encryption: we adopt industry-standard security measures to the extent reasonably feasible to reduce the risk of data breaches, tampering, and loss.
  - We will not disclose the aforementioned data to third parties unless required by laws and regulations, legitimate requests from law enforcement or regulators, or as necessary to protect the legitimate rights and interests of us or our users.

8. Security, Content, and Intellectual Property Compliance
  - You must ensure that you have legitimate administrative permissions or operational authorization for target devices.
  - You must not use the Software to infringe any third party's intellectual property, reputation, privacy, or other legitimate rights.
  - For content that you integrate or display, you bear sole responsibility for compliance and copyright.

9. Third-Party Components and Services
  The Software may rely on third-party open-source libraries or external services. The availability, accuracy, and compliance of such third parties are the responsibility of their respective providers. You should follow the corresponding licenses or terms of use and evaluate and assume the associated risks yourself.

10. Risk Disclosure
  - The Software is provided "as is" and may be subject to incompatibility, unavailability, or misuse risks due to differences in networks, hardware, or systems.
  - Remote control and batch operations carry potentially high risks. Please be sure to adopt best practices such as least privilege, tiered authorization, multi-factor authentication, audit trails, and staged environment validation.

11. Disclaimer of Warranties
  To the maximum extent permitted by applicable law, the Software and its developers make no express or implied warranties regarding the suitability, stability, correctness, availability, or fitness for a particular purpose of the Software, and assume no liability for any loss or damage arising from the use of or inability to use the Software.

12. Limitation of Liability
  In no event shall the developers of the Software be liable for any indirect, incidental, special, punitive, or consequential damages arising out of or in connection with the Software; the total liability (if any) for any direct damages shall be limited to the lowest cap permitted by applicable law.

13. Indemnification
  If you give rise to any third-party claim, lawsuit, dispute, or penalty due to your violation of this notice or applicable law, you shall bear full responsibility independently and hold the developers and contributors of the Software harmless.

14. Termination and Technical Support
  The developers reserve the right to refuse or terminate any form of technical support or assistance to users who are suspected of or actually violate this notice.

15. Export Control and Sanctions Compliance
  You undertake to comply with applicable export control, re-export, and economic sanctions laws and regulations and must not use or provide the Software to or for restricted countries, regions, entities, or individuals.

16. Notice and Revisions
  This notice may be revised as versions are updated or laws and policies change. The updated version will be published in an appropriate manner and takes effect upon publication.

17. Governing Law and Dispute Resolution
  Without prejudice to mandatory laws, the interpretation and application of this notice shall be governed by the laws of the jurisdiction where the maintainers of the open-source repository of the Software are located; disputes shall first be resolved through friendly negotiation, and if negotiation fails, they shall be submitted to a court or arbitration institution with jurisdiction.

18. Final Terms
  If you do not agree with any content of this notice, or cannot ensure that your use is fully lawful and compliant, please immediately stop using and uninstall the Software. Continued use is deemed to be your acknowledgment that you have read, understood, and agreed to all the contents of this notice.

Effective date: 2025-10-20
`,
  "ja_JP": `法的通知とコンプライアンスガイド

重要な注意
  本書は、Komari（「本ソフトウェア」）の合法的な利用範囲、ユーザーの権利義務、およびリスクに関する注意事項を明確にすることを目的としています。本ソフトウェアをダウンロード、インストール、または使用する前に、本書の全内容を必ずお読みいただきご理解ください。ダウンロード、インストール、または使用した時点で、本書の内容を理解し同意したものとみなされます。

1. 適用範囲
  本書は、本ソフトウェアおよびその派生ツール、ドキュメント、サービスを直接または間接的に取得、インストール、アクセス、または使用するすべての自然人、法人、その他の組織に適用されます。

2. 定義
  - 「あなた/ユーザー」：何らかの方法で本ソフトウェアを使用する主体を指します。
  - 「本ソフトウェア/Komari」：オープンソースとして公開されている Komari Monitor および関連コンポーネント、サンプル、スクリプト、ドキュメントを指します。
  - 「対象デバイス」：あなたが管理または操作するすべてのホスト、サーバー、仮想マシン、コンテナ、またはネットワークデバイスを指します。

3. ライセンスと利用範囲
  - 本ソフトウェアはオープンソースライセンス（MIT）に基づき使用許諾されます。MIT ライセンスに別段の定めがある場合を除き、本書はコンプライアンスおよびリスクに関する補足条項として機能します。
  - 本ソフトウェアは、合法的かつ適法で、許可を得たシステム管理、監視、研究目的にのみ使用できます。法令、規制政策、または第三者の正当な権利に違反する目的で本ソフトウェアを使用してはなりません。
  - 本ソフトウェアは、セキュリティ対策の回避、クラッキング、またはペネトレーションテストを許可する黙示の権限を提供せず、また含みません。セキュリティテストを行う前に、対象システムの事前の明示的な書面による許可を取得する必要があります。

4. ユーザーの責任（極めて重要）
  本ソフトウェアを通じて行うすべての操作について、あなたはすべての法的責任を単独で負います。使用意図が何であれ、行為が発生した場合、その結果はすべて自己責任となり、本ソフトウェアの開発者とは無関係です。開発者はあなたの使用方法を審査、監視、または是認せず、直接的、間接的、または連帯的な責任も負いません。

5. 禁止行為（これに限られません）
  5.1 許可のないコンピュータ、サーバー、ネットワーク、またはアカウントへの侵入、制御、アクセス。
  5.2 ポートスキャン、脆弱性の探索、または許可のないセキュリティテストおよび情報収集。
  5.3 サイバー攻撃（DoS/DDoS、トラフィックハイジャック、スニッフィング、中間者攻撃など）の開始または参加。
  5.4 悪意のあるコード、トロイの木馬、バックドア、ランサムウェア、またはボットネットプログラムのインストール、配布、実行。乗っ取ったデバイスを暗号通貨マイニング、スパム送信、フィッシング、詐欺に利用すること。
  5.5 個人情報、営業秘密、知的財産、その他の機密データの許可のない収集、処理、改ざん、削除、漏えい。画面覗き見、キーロガー、ファイル監視などの監視行為。
  5.6 わいせつ、暴力、テロリズム、憎悪、差別、名誉毀損、その他の不適切な内容を含む（これらに限られない）違法または権利侵害コンテンツの拡散。
  5.7 技術的保護手段の回避、リバースエンジニアリング（法律で認められる範囲を除く）、他人のシステムの安定性と可用性への干渉・破壊。

6. コンプライアンスと地域要件（特別な注意）
  あなたの使用が以下のすべてに適合することを確認する必要があります：
  - あなたの所在地の法令；
  - 対象デバイスの所在地の法令；
  - 関連する業界の監督規則と自主規制基準。
  特に、中国本土に所在する場合は、「ネットワーク安全法」「データ安全法」「個人情報保護法」「重要情報インフラ安全保護条例」などの関連法令と基準を厳守する必要があります。香港特別行政区、マカオ特別行政区、および台湾地域に所在する場合も、現地の適用法令を遵守する必要があります。違法または違反のリスクはすべて自己責任となります。

7. データとプライバシーのコンプライアンス
  - 個人情報を処理する前に、合法的かつ正当で必要な処理根拠を持ち、告知、同意、最小化、目的制限、安全保証、越境コンプライアンスなどの義務を果たす必要があります。
  - 機密データまたは秘密情報が含まれる場合は、暗号化、匿名化、アクセス制御、ログ監査などの合理的かつ必要な技術的・管理的措置を実施する必要があります。
  - 法令遵守、セキュリティ監査、悪用防止、紛争処理の目的で、適用法令が許容し必要な場合に限り、最小限のネットワークメタデータを収集・保持することがあります。このデータはセキュリティおよび法務コンプライアンス目的にのみ使用され、法的紛争を除き、あなたのデータを積極的に共有することはありません。
  - データセキュリティと暗号化：合理的に実行可能な範囲で業界標準のセキュリティ対策を講じ、データの漏えい、改ざん、消失のリスクを低減します。
  - 法令の要求、法執行機関または監督当局の正当な要求、または当社およびユーザーの正当な権益を保護するために必要な場合を除き、前述のデータを第三者に開示しません。

8. セキュリティ、コンテンツ、知的財産のコンプライアンス
  - 対象デバイスに対する正当な管理権限または操作許可を確保する必要があります。
  - 本ソフトウェアを利用して第三者の知的財産権、名誉権、プライバシー権、その他の正当な権利を侵害してはなりません。
  - あなたが組み込んだり表示したりするコンテンツについて、コンプライアンスと著作権の責任はあなたが単独で負います。

9. 第三者のコンポーネントとサービス
  本ソフトウェアは、第三者のオープンソースライブラリや外部サービスに依存する場合があります。これらの第三者の可用性、正確性、コンプライアンスは、それぞれの提供者が責任を負います。対応するライセンスまたは利用規約に従い、関連するリスクを自ら評価し負担してください。

10. リスクに関する注意
  - 本ソフトウェアは「現状のまま」提供され、ネットワーク、ハードウェア、システムの差異により、非互換、利用不可、誤用のリスクが生じる可能性があります。
  - リモートコントロールと一括操作には潜在的に高いリスクがあります。最小権限、段階的な承認、多要素認証、監査証跡、環境別の検証などのベストプラクティスを必ず採用してください。

11. 保証の否認
  適用法令が許す最大限の範囲で、本ソフトウェアおよびその開発者は、本ソフトウェアの適合性、安定性、正確性、可用性、または特定目的への適合性について、明示的または黙示的な保証を行いません。また、本ソフトウェアの使用または使用不能によって生じたいかなる種類の損失または損害についても責任を負いません。

12. 責任の制限
  いかなる場合においても、本ソフトウェアの開発者は、本ソフトウェアに起因または関連する間接的、付随的、特別、懲罰的、または結果的な損害について責任を負いません。直接損害に対する総責任（ある場合）は、適用法令が許す最低の上限を限度とします。

13. 補償条項
  本書または適用法令への違反により第三者からの主張、請求、紛争、または制裁が生じた場合、あなたはすべての責任を単独で負い、本ソフトウェアの開発者および貢献者を免責させるものとします。

14. 終了とテクニカルサポート
  本書に違反している疑いがある、または実際に違反しているユーザーに対して、開発者はあらゆる形式のテクニカルサポートまたは支援を拒否または終了する権利を有します。

15. 輸出管理と制裁のコンプライアンス
  適用される輸出管理、再輸出、経済制裁に関する法令を遵守することに同意し、制限対象の国、地域、団体、個人に対して本ソフトウェアを使用または提供してはなりません。

16. 通知と改訂
  本書は、バージョン更新や法律・政策の変更に伴い改訂されることがあります。更新版は適切な方法で公表され、公表日から効力を生じます。

17. 準拠法と紛争解決
  強行法規に抵触しない限り、本書の解釈と適用は、本ソフトウェアのオープンソースリポジトリのメンテナー所在地の法律に従います。紛争は友好的な協議により解決するものとし、協議が成立しない場合は、管轄権を有する裁判所または仲裁機関に付託します。

18. 最終条項
  本書の内容に同意しない場合、または使用が完全に合法かつコンプライアンスに適合することを確実にできない場合は、直ちに使用を中止し、本ソフトウェアをアンインストールしてください。使用を継続した場合、本書の全内容を読了し、理解し、同意したものとみなされます。

発効日：2025-10-20
`,
  "id_ID": `Pemberitahuan Hukum dan Panduan Kepatuhan

Penting
  Dokumen ini bertujuan untuk menjelaskan batas penggunaan Komari ("Perangkat Lunak") secara sah, hak dan kewajiban pengguna, serta peringatan risiko. Sebelum mengunduh, menginstal, atau menggunakan Perangkat Lunak, harap baca dan pahami seluruh isi pemberitahuan ini. Dengan mengunduh, menginstal, atau menggunakan Perangkat Lunak, Anda dianggap telah memahami dan menyetujui untuk terikat oleh pemberitahuan ini.

1. Ruang Lingkup
  Pemberitahuan ini berlaku untuk semua orang perseorangan, badan hukum, atau organisasi lain yang secara langsung atau tidak langsung memperoleh, menginstal, mengakses, atau menggunakan Perangkat Lunak beserta alat turunan, dokumentasi, dan layanannya.

2. Definisi
  - "Anda/Pengguna": pihak mana pun yang menggunakan Perangkat Lunak dengan cara apa pun.
  - "Perangkat Lunak/Komari": Komari Monitor yang dirilis sebagai sumber terbuka beserta komponen, contoh, skrip, dan dokumentasi terkait.
  - "Perangkat Target": setiap host, server, mesin virtual, kontainer, atau perangkat jaringan yang Anda kelola atau operasikan.

3. Lisensi dan Batas Penggunaan
  - Perangkat Lunak dilisensikan berdasarkan lisensi sumber terbuka (MIT). Kecuali ditentukan lain oleh lisensi MIT, pemberitahuan ini berfungsi sebagai ketentuan tambahan untuk kepatuhan dan peringatan risiko.
  - Perangkat Lunak hanya boleh digunakan untuk tujuan administrasi sistem, pemantauan, dan penelitian yang sah, patuh, dan telah diotorisasi. Anda tidak boleh menggunakan Perangkat Lunak untuk tindakan apa pun yang melanggar hukum, peraturan, kebijakan regulasi, atau hak sah pihak ketiga.
  - Perangkat Lunak tidak memberikan atau mengandung otorisasi tersirat untuk melewati kontrol keamanan, meretas, atau melakukan uji penetrasi. Sebelum melakukan pengujian keamanan, Anda harus memperoleh otorisasi tertulis eksplisit terlebih dahulu dari pemilik sistem yang diuji.

4. Tanggung Jawab Pengguna (Sangat Penting)
  Anda menanggung seluruh dan satu-satunya tanggung jawab hukum atas semua operasi yang dilakukan melalui Perangkat Lunak. Apa pun niat penggunaan Anda, semua konsekuensi yang timbul adalah tanggung jawab Anda sepenuhnya dan tidak terkait dengan pengembang Perangkat Lunak. Pengembang tidak meninjau, memantau, atau mendukung cara penggunaan Anda, dan tidak menanggung tanggung jawab langsung, tidak langsung, atau tanggung renteng.

5. Perilaku yang Dilarang (Termasuk namun Tidak Terbatas Pada)
  5.1 Intrusi, penguasaan, atau akses tanpa izin ke komputer, server, jaringan, atau akun mana pun.
  5.2 Pemindaian port, pendeteksian kerentanan, atau pengujian keamanan dan pengumpulan informasi tanpa izin.
  5.3 Melancarkan atau berpartisipasi dalam serangan siber (misalnya DoS/DDoS, pembajakan lalu lintas, sniffing, serangan man-in-the-middle).
  5.4 Menginstal, mendistribusikan, atau menjalankan kode berbahaya, trojan, backdoor, ransomware, atau program botnet; memanfaatkan perangkat yang dikuasai untuk penambangan kripto, spam, phishing, atau penipuan.
  5.5 Mengumpulkan, memproses, mengubah, menghapus, atau membocorkan informasi pribadi, rahasia dagang, kekayaan intelektual, atau data sensitif lainnya tanpa izin; melakukan tindakan pengawasan seperti mengintip layar, keylogging, atau memantau file.
  5.6 Menyebarkan konten ilegal atau melanggar hak, termasuk namun tidak terbatas pada konten cabul, kekerasan, terorisme, kebencian, diskriminasi, pencemaran nama baik, atau konten tidak pantas lainnya.
  5.7 Menghindari tindakan perlindungan teknis, rekayasa balik (kecuali diizinkan oleh hukum), mengganggu/merusak stabilitas dan ketersediaan sistem orang lain.

6. Kepatuhan dan Persyaratan Wilayah (Catatan Khusus)
  Anda harus memastikan penggunaan Anda memenuhi:
  - hukum dan peraturan di lokasi Anda;
  - hukum dan peraturan di lokasi perangkat target;
  - aturan pengawasan industri dan standar pengaturan mandiri terkait.
  Khususnya, jika Anda berada di Tiongkok Daratan, Anda wajib mematuhi Undang-Undang Keamanan Siber, Undang-Undang Keamanan Data, Undang-Undang Perlindungan Informasi Pribadi, Peraturan Perlindungan Keamanan Infrastruktur Informasi Vital, serta hukum, peraturan, dan standar terkait lainnya. Jika Anda berada di Wilayah Administratif Khusus Hong Kong, Wilayah Administratif Khusus Makau, atau Wilayah Taiwan, Anda juga wajib mematuhi hukum dan peraturan lokal yang berlaku. Segala risiko akibat tindakan ilegal atau tidak patuh ditanggung sendiri oleh Anda.

7. Kepatuhan Data dan Privasi
  - Sebelum memproses informasi pribadi apa pun, Anda harus memiliki dasar pemrosesan yang sah, wajar, dan diperlukan, serta memenuhi kewajiban seperti pemberitahuan, persetujuan, minimalisasi, pembatasan tujuan, jaminan keamanan, dan kepatuhan lintas batas.
  - Jika melibatkan data sensitif atau informasi rahasia, Anda harus menerapkan langkah teknis dan manajerial yang wajar dan diperlukan seperti enkripsi, desensitisasi, kontrol akses, dan audit log.
  - Untuk tujuan kepatuhan hukum, audit keamanan, pencegahan penyalahgunaan, dan penanganan sengketa, serta jika diizinkan dan diperlukan oleh hukum yang berlaku, kami dapat mengumpulkan dan menyimpan metadata jaringan yang minimal. Data tersebut hanya digunakan untuk tujuan keamanan dan kepatuhan hukum; kami tidak akan membagikan data Anda secara aktif kecuali terkait sengketa hukum.
  - Keamanan dan enkripsi data: kami menerapkan langkah keamanan standar industri sejauh yang wajar untuk mengurangi risiko kebocoran, pengubahan, dan kehilangan data.
  - Kami tidak akan mengungkapkan data tersebut kepada pihak ketiga kecuali diwajibkan oleh hukum dan peraturan, permintaan sah dari penegak hukum atau regulator, atau diperlukan untuk melindungi hak sah kami dan pengguna.

8. Kepatuhan Keamanan, Konten, dan Kekayaan Intelektual
  - Anda harus memastikan memiliki izin manajemen atau otorisasi operasi yang sah atas perangkat target.
  - Dilarang menggunakan Perangkat Lunak untuk melanggar kekayaan intelektual, hak reputasi, hak privasi, atau hak sah lainnya milik pihak ketiga.
  - Untuk konten yang Anda integrasikan atau tampilkan, Anda menanggung sendiri tanggung jawab kepatuhan dan hak cipta.

9. Komponen dan Layanan Pihak Ketiga
  Perangkat Lunak mungkin bergantung pada pustaka sumber terbuka pihak ketiga atau layanan eksternal. Ketersediaan, keakuratan, dan kepatuhan pihak ketiga tersebut menjadi tanggung jawab penyedianya masing-masing. Anda harus mengikuti lisensi atau ketentuan penggunaan terkait, serta mengevaluasi dan menanggung risiko yang terkait sendiri.

10. Peringatan Risiko
  - Perangkat Lunak disediakan "sebagaimana adanya" dan dapat menimbulkan risiko ketidakcocokan, ketidaktersediaan, atau penyalahgunaan karena perbedaan jaringan, perangkat keras, atau sistem.
  - Kontrol jarak jauh dan operasi massal memiliki potensi risiko tinggi. Pastikan Anda menerapkan praktik terbaik seperti hak akses minimal, otorisasi berjenjang, autentikasi multi-faktor, jejak audit, dan validasi per lingkungan.

11. Penyangkalan Jaminan
  Sejauh diizinkan oleh hukum yang berlaku: Perangkat Lunak dan pengembangnya tidak memberikan jaminan tersurat maupun tersirat apa pun mengenai kesesuaian, stabilitas, kebenaran, ketersediaan, atau kesesuaian untuk tujuan tertentu; dan tidak bertanggung jawab atas segala bentuk kerugian atau kerusakan yang timbul akibat penggunaan atau ketidakmampuan menggunakan Perangkat Lunak.

12. Batasan Tanggung Jawab
  Dalam keadaan apa pun, pengembang Perangkat Lunak tidak bertanggung jawab atas kerusakan tidak langsung, insidental, khusus, punitif, atau konsekuensial yang timbul dari atau terkait dengan Perangkat Lunak; total tanggung jawab atas kerusakan langsung (jika ada) dibatasi pada batas terendah yang diizinkan oleh hukum yang berlaku.

13. Ketentuan Ganti Rugi
  Jika Anda menimbulkan klaim, tuntutan, sengketa, atau hukuman dari pihak ketiga karena melanggar pemberitahuan ini atau hukum yang berlaku, Anda menanggung seluruh tanggung jawab secara mandiri dan membebaskan pengembang serta kontributor Perangkat Lunak dari kerugian.

14. Penghentian dan Dukungan Teknis
  Terhadap pengguna yang diduga atau terbukti melanggar pemberitahuan ini, pengembang berhak menolak atau menghentikan segala bentuk dukungan atau bantuan teknis.

15. Kepatuhan Kontrol Ekspor dan Sanksi
  Anda berjanji mematuhi hukum dan peraturan kontrol ekspor, re-ekspor, dan sanksi ekonomi yang berlaku, dan tidak menggunakan atau memberikan Perangkat Lunak kepada atau untuk negara, wilayah, entitas, atau individu yang dibatasi.

16. Pemberitahuan dan Revisi
  Pemberitahuan ini dapat direvisi seiring pembaruan versi atau perubahan hukum dan kebijakan. Versi terbaru akan diumumkan dengan cara yang tepat dan berlaku sejak tanggal pengumuman.

17. Hukum yang Berlaku dan Penyelesaian Sengketa
  Tanpa mengurangi hukum yang bersifat memaksa, interpretasi dan penerapan pemberitahuan ini tunduk pada hukum lokasi pemelihara repositori sumber terbuka Perangkat Lunak; sengketa harus diselesaikan melalui negosiasi yang baik, dan jika tidak tercapai, diajukan ke pengadilan atau lembaga arbitrase yang berwenang.

18. Ketentuan Akhir
  Jika Anda tidak menyetujui bagian mana pun dari pemberitahuan ini, atau tidak dapat memastikan penggunaan Anda sepenuhnya sah dan patuh, segera hentikan penggunaan dan hapus instalan Perangkat Lunak. Penggunaan berkelanjutan dianggap sebagai persetujuan bahwa Anda telah membaca, memahami, dan menyetujui seluruh isi pemberitahuan ini.

Tanggal berlaku: 2025-10-20
`,
};

export function getEula(language: string): string {
  return resolveI18nText(EULAS, language) ?? EULAS.zh_CN;
}
