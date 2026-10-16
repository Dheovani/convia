import type { Words } from "./en";

// Everything the interface says, in Brazilian Portuguese.
export const ptBR: Words = {
  brand: "Convia",
  failure: {
    message: "Algo deu errado.",
    reload: "Recarregar",
  },
  refusals: {
    name: "Um nome tem de 1 a 32 caracteres.",
    "weak-password": "A senha precisa de pelo menos 12 caracteres.",
    password: "Essa senha não abre este arquivo.",
    "export-file":
      "Este não é uma identidade exportada da Convia, ou está danificado.",
    newer: "Estes dados são de uma Convia mais nova.",
    "no-identity": "Ainda não há uma identidade.",
    "invitation-checksum":
      "Este código não foi copiado inteiro, ou exatamente.",
    "invitation-malformed": "Este não é um convite da Convia.",
    "invitation-newer":
      "Este convite foi feito por uma Convia mais nova. Atualize a Convia para usá-lo.",
    "invitation-signature": "Este convite foi alterado depois de feito.",
    "invitation-yours": "Este é o seu próprio convite.",
    "already-contact": "Esta pessoa já é um dos seus contatos.",
    blocked: "Você bloqueou esta pessoa. Desbloqueie-a antes.",
    "no-such-contact": "Esse contato ou pedido não existe mais.",
    "message-too-long": "Uma mensagem tem no máximo 4.000 caracteres.",
    "message-empty": "Não há nada para enviar.",
    unknown: "Algo deu errado.",
  },
  home: {
    contacts: "Contatos",
    you: "Você",
    outdatedTheirs: (name: string) =>
      `${name} usa uma Convia mais antiga. Vocês voltam a conversar quando ela for atualizada.`,
    outdatedMine: (name: string) =>
      `${name} usa uma Convia mais nova. Atualize a Convia para conversar.`,
    someone: "Um contato",
  },
  contacts: {
    invitationTitle: "Seu convite",
    invitationExplanation:
      "Dê este código a quem deve poder pedir para ser seu contato. Qualquer pessoa com ele pode pedir; só quem você aceitar vira contato.",
    copy: "Copiar",
    copied: "Copiado.",
    addTitle: "Adicionar um contato",
    addExplanation:
      "Cole o convite que a pessoa lhe deu. Ela vê o seu pedido e escolhe se aceita.",
    code: "O convite dela",
    ask: "Enviar o pedido",
    asked: (name: string) => `Pedido enviado para ${name}.`,
    safetyCode: "Código de segurança",
    safetyHint:
      "Compare-o com a pessoa, pessoalmente ou numa chamada. Se for igual, ninguém está no meio.",
    received: "Pedindo para ser seu contato",
    accept: "Aceitar",
    decline: "Recusar",
    block: "Bloquear",
    sent: "Seus pedidos",
    waiting: "Aguardando resposta",
    notAccepted: "Não aceito",
    dismiss: "Descartar",
    list: "Seus contatos",
    none: "Ainda não há contatos.",
    verified: "Verificado",
    verify: (name: string) => `Comparei este código com ${name}`,
    remove: "Remover",
    forget: "Apagar também a nossa conversa",
    removeHint: "A pessoa não é avisada.",
    blocked: "Bloqueados",
    unblock: "Desbloquear",
    open: "Abrir a conversa",
  },
  workspace: {
    directMessages: "Mensagens diretas",
    contacts: "Contatos",
    settings: "Configurações",
    home: "Mensagens diretas",
    conversations: "Conversas",
    unread: (count: number) => `${count} não lidas`,
    requests: (count: number) => `${count} aguardando a sua resposta`,
    noConversations:
      "Ainda não há com quem conversar. Adicione um contato com o convite dele.",
    choose: "Escolha uma conversa.",
    you: "Você",
    verified: "Verificado",
  },
  settings: {
    title: "Configurações",
    language: "Idioma",
    languageSystem: (name: string) => `Como o sistema (${name})`,
    theme: "Tema",
    themes: { system: "Como o sistema", dark: "Escuro", light: "Claro" },
    notificationsTitle: "Notificações",
    notificationText: "Mostrar o que a mensagem diz na notificação",
    notificationTextHint:
      "Desligado, a notificação diz só quem escreveu, para ninguém perto da tela ler.",
  },
  tray: {
    open: "Abrir a Convia",
    autostart: "Iniciar com o Windows",
    quit: "Sair da Convia",
  },
  notification: {
    title: (name: string) => `Nova mensagem de ${name}`,
  },
  link: {
    title: "Abrir este link?",
    explanation: "Ele abre no seu navegador, e o site vai ver o acesso.",
    open: "Abrir",
    cancel: "Cancelar",
  },
  conversation: {
    back: "Voltar",
    none: "Nada foi dito ainda.",
    field: (name: string) => `Mensagem para ${name}`,
    send: "Enviar",
    sendHint: "Enter envia; Shift+Enter começa uma nova linha.",
    left: (count: number) => `Restam ${count} caracteres`,
    over: (count: number) => `${count} caracteres a mais`,
    waiting: "Aguardando",
    sent: "Enviada",
    delivered: "Entregue",
    waitingHint: (name: string) =>
      `Ela espera neste computador até ${name} ficar online.`,
  },
  welcome: {
    title: "Boas-vindas à Convia",
    explanation:
      "A Convia cria a sua identidade aqui, neste computador. Ninguém mais a emite nem a guarda.",
    name: "Seu nome",
    nameHint: "O que os seus contatos veem. Não precisa ser único.",
    create: "Criar minha identidade",
    haveOne: "Tenho uma identidade exportada",
  },
  importing: {
    title: "Importar uma identidade",
    chooseFile: "Escolher o arquivo",
    noFile: "Nenhum arquivo escolhido",
    password: "A senha dele",
    import: "Importar",
    replaceWarning:
      "Este computador já tem uma identidade. Importar a guarda, com o histórico, numa pasta própria; nada é apagado.",
    replace: "Guardar a minha e importar",
    cancel: "Cancelar",
  },
  me: {
    name: "Nome",
    rename: "Mudar o nome",
    save: "Salvar",
    publicKey: "Sua chave pública",
    publicKeyHint:
      "É quem você é para os seus contatos. O nome pode mudar; isto não.",
    exportTitle: "Exportar sua identidade",
    exportExplanation:
      "Guarde uma cópia, protegida por senha, para levar a sua identidade a outro computador. Sem ela, perder este computador é perder a sua identidade.",
    password: "Senha",
    confirmPassword: "A mesma senha de novo",
    mismatch: "As duas senhas são diferentes.",
    export: "Exportar",
    exported: "Exportada.",
    deleteTitle: "Apagar sua identidade",
    deleteExplanation:
      "Isto destrói a sua identidade, os seus contatos e as suas conversas neste computador. Os seus contatos não são avisados. Exporte antes, se puder querê-la de volta.",
    deleteConfirm: "Entendo que isto não pode ser desfeito",
    delete: "Apagar minha identidade",
  },
  dataProblem: {
    title: {
      newer: "Estes dados são de uma Convia mais nova.",
      damaged: "A Convia não consegue ler os seus dados.",
      "key-unreadable": "A Convia não consegue desbloquear os seus dados.",
      other: "A Convia não consegue abrir os seus dados.",
    },
    explanation: {
      newer:
        "Eles foram gravados por uma versão mais nova da Convia. Atualize a Convia para abri-los; nada foi alterado.",
      damaged: "O arquivo que guarda as suas conversas está danificado.",
      "key-unreadable":
        "A chave deles não abre mais, o que acontece quando a conta do Windows a que pertenciam mudou.",
      other: "Algo impediu a Convia de abrir os seus dados. O log diz o quê.",
    },
    keptAside:
      "Começar do zero guarda os arquivos ilegíveis numa pasta própria; nada é apagado.",
    startOver: "Começar do zero",
    importInstead: "Importar uma identidade exportada",
  },
};
